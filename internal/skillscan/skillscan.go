// Package skillscan vets agent skills with NVIDIA SkillSpector, run in a
// container so the host needs no Python.
package skillscan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// SourceRepo and SourceCommit pin SkillSpector v2.13.4. NVIDIA publishes no
	// image, so it is built locally. Podman cannot build from a git tag
	// context, only from a commit.
	SourceRepo   = "https://github.com/NVIDIA/skillspector.git"
	SourceCommit = "54824463814a0be01360792523ec7245a69190fc"
	DefaultImage = "localhost/convergence-skillspector:v2.13.4"

	// LastScanFile (in ~/.convergence) caches the last static scan.
	LastScanFile = "skill-scan.json"

	// RecommendBlock is SkillSpector's verdict for a skill not to install.
	RecommendBlock = "DO_NOT_INSTALL"
)

// skillRoots are the global skill dirs of the agents Convergence configures.
var skillRoots = []string{".claude/skills", ".config/opencode/skills", ".agents/skills", ".hermes/skills"}

// Discover lists every installed skill dir under the known roots in home.
func Discover(home string) []string {
	var out []string
	for _, r := range skillRoots {
		out = append(out, SkillDirs(filepath.Join(home, r))...)
	}
	return out
}

// SkillDirs returns root itself when it holds a SKILL.md, else the skill dirs
// one or two levels below it (Hermes groups skills in category dirs).
func SkillDirs(root string) []string {
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err == nil {
		return []string{root}
	}
	var out []string
	for _, pat := range []string{"*/SKILL.md", "*/*/SKILL.md"} {
		m, _ := filepath.Glob(filepath.Join(root, pat))
		for _, f := range m {
			out = append(out, filepath.Dir(f))
		}
	}
	return out
}

// Fingerprint changes when any file in dirs is added, removed, resized or touched.
func Fingerprint(dirs []string) string {
	h := sha256.New()
	for _, d := range dirs {
		_ = filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			if info, err := e.Info(); err == nil {
				_, _ = fmt.Fprintf(h, "%s\x00%d\x00%d\n", p, info.Size(), info.ModTime().UnixNano())
			}
			return nil
		})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// LLM routes SkillSpector's semantic stage through a Convergence gateway.
// Without it the scan is static only and runs with no network.
type LLM struct {
	BaseURL string // OpenAI-compatible, ending in /v1
	Model   string // must honor forced tool calls (function_calling)
	Network string // container network; empty = default bridge with host-gateway
}

// Skill is one scanned skill.
type Skill struct {
	Name           string `json:"name"`
	Dir            string `json:"dir"`
	Score          int    `json:"score"`
	Severity       string `json:"severity"`
	Recommendation string `json:"recommendation"`
}

// Report is the outcome of one scan.
type Report struct {
	ScannedAt   time.Time `json:"scanned_at"`
	Image       string    `json:"image"`
	Model       string    `json:"model,omitempty"`
	Fingerprint string    `json:"fingerprint"`
	Complete    bool      `json:"complete"`
	Skills      []Skill   `json:"skills"`
}

// Unsafe lists the skills SkillSpector says not to install.
func (r *Report) Unsafe() []Skill {
	var out []Skill
	for _, s := range r.Skills {
		if s.Recommendation == RecommendBlock {
			out = append(out, s)
		}
	}
	return out
}

// Scanner runs SkillSpector through a container engine.
type Scanner struct {
	Engine string // docker or podman
	Image  string
	// Exec runs c and returns stdout and the exit code; replaceable in tests.
	Exec func(c *exec.Cmd) ([]byte, int, error)
}

// New returns a Scanner using image, or DefaultImage when empty.
func New(engine, image string) *Scanner {
	if image == "" {
		image = DefaultImage
	}
	return &Scanner{Engine: engine, Image: image, Exec: execCmd}
}

func execCmd(c *exec.Cmd) ([]byte, int, error) {
	var stderr bytes.Buffer
	if c.Stderr == nil {
		c.Stderr = &stderr
	}
	out, err := c.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out, ee.ExitCode(), fmt.Errorf("%w: %s", err, lastLine(stderr.String()))
	}
	return out, 0, err
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// ImagePresent reports whether the scanner image exists locally.
func (s *Scanner) ImagePresent(ctx context.Context) bool {
	_, code, err := s.Exec(exec.CommandContext(ctx, s.Engine, "image", "inspect", s.Image))
	return err == nil && code == 0
}

// Build builds the pinned SkillSpector image, streaming progress to w.
func (s *Scanner) Build(ctx context.Context, w io.Writer) error {
	c := exec.CommandContext(ctx, s.Engine, "build", "-t", s.Image, SourceRepo+"#"+SourceCommit)
	c.Stderr = w
	if _, _, err := s.Exec(c); err != nil {
		return fmt.Errorf("building %s: %w", s.Image, err)
	}
	return nil
}

// RunArgs mounts every skill dir read-only under /scan/<index>-<name> so one
// recursive scan covers them all.
func (s *Scanner) RunArgs(dirs []string, llm *LLM, volSuffix string) []string {
	args := []string{"run", "--rm"}
	switch {
	case llm == nil:
		args = append(args, "--network=none")
	case llm.Network != "":
		args = append(args, "--network="+llm.Network)
	default:
		args = append(args, "--add-host=host.docker.internal:host-gateway")
	}
	for i, d := range dirs {
		args = append(args, "-v", fmt.Sprintf("%s:/scan/%03d-%s:ro%s", d, i, filepath.Base(d), volSuffix))
	}
	if llm != nil {
		args = append(args,
			"-e", "SKILLSPECTOR_PROVIDER=openai_compatible",
			"-e", "SKILLSPECTOR_COMPAT_BASE_URL="+llm.BaseURL,
			"-e", "SKILLSPECTOR_COMPAT_API_KEY=convergence",
			"-e", "SKILLSPECTOR_MODEL="+llm.Model,
			// Gateway-proxied models often ignore json_schema; forced tool calls work.
			"-e", "SKILLSPECTOR_STRUCTURED_OUTPUT_METHOD=function_calling")
	}
	args = append(args, s.Image, "scan", "/scan", "--recursive", "--format", "json")
	if llm == nil {
		args = append(args, "--no-llm")
	}
	return args
}

// Scan scans dirs. SkillSpector exits 1 on findings, which is not an error.
func (s *Scanner) Scan(ctx context.Context, dirs []string, llm *LLM, volSuffix string) (*Report, error) {
	if len(dirs) == 0 {
		return nil, errors.New("no skills found to scan")
	}
	out, code, err := s.Exec(exec.CommandContext(ctx, s.Engine, s.RunArgs(dirs, llm, volSuffix)...))
	if code != 0 && code != 1 {
		return nil, fmt.Errorf("skillspector failed (exit %d): %w", code, err)
	}
	rep, perr := parse(out, dirs)
	if perr != nil {
		return nil, perr
	}
	rep.ScannedAt, rep.Image, rep.Fingerprint = time.Now().UTC(), s.Image, Fingerprint(dirs)
	if llm != nil {
		rep.Model = llm.Model
	}
	return rep, nil
}

// raw is the subset of SkillSpector's multi-skill JSON report we read.
type raw struct {
	Completeness struct {
		Status string `json:"status"`
	} `json:"analysis_completeness"`
	Skills []struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Score    int    `json:"risk_score"`
		Severity string `json:"risk_severity"`
		Risk     struct {
			Recommendation string `json:"recommendation"`
		} `json:"risk_assessment"`
	} `json:"skills"`
}

func parse(out []byte, dirs []string) (*Report, error) {
	var r raw
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("reading skillspector report: %w", err)
	}
	rep := &Report{Complete: r.Completeness.Status == "complete"}
	for _, s := range r.Skills {
		sk := Skill{Name: s.Name, Score: s.Score, Severity: s.Severity, Recommendation: s.Risk.Recommendation}
		// Map the /scan/<index>-<name> mount back to the host dir.
		if i, err := strconv.Atoi(strings.SplitN(filepath.Base(s.Path), "-", 2)[0]); err == nil && i < len(dirs) {
			sk.Dir = dirs[i]
		}
		rep.Skills = append(rep.Skills, sk)
	}
	return rep, nil
}

// Load reads a cached report; a missing file returns nil, nil.
func Load(path string) (*Report, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return &r, nil
}

// Save writes r to path (0600: it lists local paths).
func Save(path string, r *Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
