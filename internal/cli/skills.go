package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jeefy/convergence/internal/config"
	"github.com/jeefy/convergence/internal/engine"
	"github.com/jeefy/convergence/internal/skillscan"
	"github.com/jeefy/convergence/internal/templates"
)

var skillsScanFlags struct {
	llm    bool
	model  string
	build  bool
	noSave bool
}

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Vet agent skills with NVIDIA SkillSpector",
	Long: `Scan agent skills for prompt injection, data exfiltration, supply-chain
and other risks with NVIDIA SkillSpector, run in a container.

Without paths, scans the global skill dirs of the agents Convergence
configures: ~/.claude/skills, ~/.config/opencode/skills, ~/.agents/skills
and <hermes home>/skills ($HERMES_HOME, else ~/.hermes).

The static scan runs with no network. --llm adds SkillSpector's semantic
analysis through this Convergence gateway (1-2 minutes per skill).`,
}

var skillsBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the pinned SkillSpector image (NVIDIA publishes none)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, eng, err := scanEngine()
		if err != nil {
			return err
		}
		s := skillscan.New(eng.Name(), cfg.ImageSkillSpector)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Building %s from %s@%s ...\n", s.Image, skillscan.SourceRepo, skillscan.SourceCommit[:12])
		return s.Build(cmd.Context(), cmd.ErrOrStderr())
	},
}

var skillsScanCmd = &cobra.Command{
	Use:   "scan [path...]",
	Short: "Scan skill dirs (default: installed skills of known agents)",
	Long: `Scan skill dirs and print SkillSpector's verdict per skill. Exits 1 when
a skill is DO_NOT_INSTALL, or when an --llm scan is incomplete.

A path may be one skill (a dir with SKILL.md) or a dir of skills.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, eng, err := scanEngine()
		if err != nil {
			return err
		}
		dirs, err := scanDirs(args)
		if err != nil {
			return err
		}
		s := skillscan.New(eng.Name(), cfg.ImageSkillSpector)
		if err := ensureImage(cmd.Context(), s, skillsScanFlags.build, cmd.ErrOrStderr()); err != nil {
			return err
		}
		var llm *skillscan.LLM
		if skillsScanFlags.llm {
			if llm, err = scanLLM(cfg, skillsScanFlags.model); err != nil {
				return err
			}
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Scanning %d skill(s)%s ...\n", len(dirs), map[bool]string{true: " with LLM analysis (" + skillsScanFlags.model + ")", false: " (static)"}[llm != nil])
		rep, err := s.Scan(cmd.Context(), dirs, llm, templates.VolumeSuffix())
		if err != nil {
			return err
		}
		printReport(out, rep)
		// The cache feeds sandbox run and doctor, which only cover default dirs.
		if len(args) == 0 && llm == nil && !skillsScanFlags.noSave {
			if err := saveScan(rep); err != nil {
				return err
			}
		}
		if bad := rep.Unsafe(); len(bad) > 0 {
			return fmt.Errorf("%d skill(s) marked DO_NOT_INSTALL", len(bad))
		}
		if llm != nil && !rep.Complete {
			return errors.New("LLM analysis incomplete: the model likely ignored forced tool calls; try another --model")
		}
		return nil
	},
}

func scanEngine() (*config.Config, *engine.ComposeEngine, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	eng, err := engine.DetectEngine(cfg.Engine)
	return cfg, eng, err
}

func scanDirs(args []string) ([]string, error) {
	if len(args) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dirs := skillscan.Discover(home)
		if len(dirs) == 0 {
			return nil, errors.New("no installed skills found; pass a path to scan")
		}
		return dirs, nil
	}
	var dirs []string
	for _, a := range args {
		abs, err := filepath.Abs(a)
		if err != nil {
			return nil, err
		}
		found := skillscan.SkillDirs(abs)
		if len(found) == 0 {
			return nil, fmt.Errorf("%s: no SKILL.md found", a)
		}
		dirs = append(dirs, found...)
	}
	return dirs, nil
}

func ensureImage(ctx context.Context, s *skillscan.Scanner, build bool, w io.Writer) error {
	if s.ImagePresent(ctx) {
		return nil
	}
	if !build {
		return fmt.Errorf("scanner image %s not found: run 'convergence skills build' (or pass --build)", s.Image)
	}
	_, _ = fmt.Fprintf(w, "Building %s (first run, a few minutes) ...\n", s.Image)
	return s.Build(ctx, w)
}

func scanLLM(cfg *config.Config, model string) (*skillscan.LLM, error) {
	if model == "" {
		return nil, errors.New("--llm needs --model (or skill_scan_model in config.yaml): a model that honors forced tool calls; 'auto' does not")
	}
	// From a container, the gateway's host port is reached via host-gateway.
	return &skillscan.LLM{BaseURL: fmt.Sprintf("http://host.docker.internal:%d/v1", cfg.GatewayPort), Model: model}, nil
}

func saveScan(rep *skillscan.Report) error {
	dir, err := config.GetConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return skillscan.Save(filepath.Join(dir, skillscan.LastScanFile), rep)
}

func printReport(w io.Writer, rep *skillscan.Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SKILL\tSCORE\tSEVERITY\tVERDICT\tPATH")
	for _, s := range rep.Skills {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", s.Name, s.Score, s.Severity, s.Recommendation, s.Dir)
	}
	_ = tw.Flush()
	if !rep.Complete {
		_, _ = fmt.Fprintln(w, "WARNING: analysis incomplete; a SAFE verdict is not conclusive.")
	}
}

// preflightSkills runs before `sandbox run` per skill_scan (default off): it
// rescans when the installed skills changed since the cached static scan.
func preflightSkills(ctx context.Context, cfg *config.Config, w io.Writer) error {
	mode := cfg.SkillScanOrDefault()
	if mode == config.SkillScanOff {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dirs := skillscan.Discover(home)
	if len(dirs) == 0 {
		return nil
	}
	rep, err := cachedScan(ctx, cfg, dirs, w)
	if err != nil {
		if mode == config.SkillScanBlock {
			return fmt.Errorf("skill scan (skill_scan: block): %w", err)
		}
		_, _ = fmt.Fprintf(w, "WARNING: skill scan skipped: %v\n", err)
		return nil
	}
	bad := rep.Unsafe()
	if len(bad) == 0 {
		return nil
	}
	names := make([]string, len(bad))
	for i, s := range bad {
		names[i] = s.Name + " (" + s.Dir + ")"
	}
	msg := fmt.Sprintf("SkillSpector marks %d installed skill(s) DO_NOT_INSTALL: %s. Details: convergence skills scan", len(bad), strings.Join(names, ", "))
	if mode == config.SkillScanBlock {
		return errors.New(msg)
	}
	_, _ = fmt.Fprintln(w, "WARNING: "+msg)
	return nil
}

func cachedScan(ctx context.Context, cfg *config.Config, dirs []string, w io.Writer) (*skillscan.Report, error) {
	dir, err := config.GetConfigDir()
	if err != nil {
		return nil, err
	}
	cache := filepath.Join(dir, skillscan.LastScanFile)
	if rep, err := skillscan.Load(cache); err == nil && rep != nil && rep.Fingerprint == skillscan.Fingerprint(dirs) {
		return rep, nil
	}
	eng, err := engine.DetectEngine(cfg.Engine)
	if err != nil {
		return nil, err
	}
	s := skillscan.New(eng.Name(), cfg.ImageSkillSpector)
	if !s.ImagePresent(ctx) {
		return nil, fmt.Errorf("scanner image %s not found; run 'convergence skills build' or set skill_scan: off", s.Image)
	}
	_, _ = fmt.Fprintf(w, "Installed skills changed; scanning %d skill(s) (static) ...\n", len(dirs))
	rep, err := s.Scan(ctx, dirs, nil, templates.VolumeSuffix())
	if err != nil {
		return nil, err
	}
	return rep, skillscan.Save(cache, rep)
}

func init() {
	f := skillsScanCmd.Flags()
	f.BoolVar(&skillsScanFlags.llm, "llm", false, "Add SkillSpector's LLM analysis through the Convergence gateway (slow)")
	f.StringVar(&skillsScanFlags.model, "model", "", "Gateway model for --llm; must honor forced tool calls (default: skill_scan_model)")
	f.BoolVar(&skillsScanFlags.build, "build", false, "Build the scanner image if missing")
	f.BoolVar(&skillsScanFlags.noSave, "no-save", false, "Do not update the cached result used by sandbox run and doctor")
	skillsScanCmd.PreRun = func(cmd *cobra.Command, args []string) {
		if skillsScanFlags.model == "" {
			if cfg, err := config.LoadConfig(); err == nil {
				skillsScanFlags.model = cfg.SkillScanModel
			}
		}
	}
	skillsCmd.AddCommand(skillsScanCmd, skillsBuildCmd)
	rootCmd.AddCommand(skillsCmd)
}
