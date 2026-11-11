package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jeefy/convergence/internal/config"
	"github.com/jeefy/convergence/internal/engine"
	"github.com/jeefy/convergence/internal/skillscan"
)

// CheckConfig verifies config.yaml exists and parses and that .env is 0600.
// The loaded config is nil when the config is unusable.
func CheckConfig(dir string) (Result, *config.Config) {
	const name = "config"
	cfgPath := filepath.Join(dir, "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return Result{name, Fail, fmt.Sprintf("cannot read %s: %v", cfgPath, err), "run 'convergence init'"}, nil
	}
	var probe config.Config
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return Result{name, Fail, fmt.Sprintf("%s does not parse: %v", cfgPath, err), "fix the YAML or re-run 'convergence init'"}, nil
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return Result{name, Fail, err.Error(), "fix the YAML or re-run 'convergence init'"}, nil
	}
	envPath := filepath.Join(dir, ".env")
	st, err := os.Stat(envPath)
	switch {
	case err != nil:
		return Result{name, Fail, fmt.Sprintf("config parses but %s is missing: %v", envPath, err), "run 'convergence init' to regenerate secrets"}, cfg
	case st.Mode().Perm() != 0o600:
		return Result{name, Fail, fmt.Sprintf("%s has mode %04o, want 0600", envPath, st.Mode().Perm()), "chmod 600 " + envPath}, cfg
	}
	return Result{name, Pass, fmt.Sprintf("%s parses (provider=%s, engine=%s); .env is 0600", cfgPath, cfg.Provider, cfg.Engine), ""}, cfg
}

// CheckEngine verifies the container engine answers and compose is available.
func CheckEngine(ctx context.Context, e *Env) Result {
	const name = "container engine"
	bin := e.Cfg.Engine
	if bin == "" {
		bin = "docker"
	}
	if err := e.Cfg.ValidateComposeProvider(); err != nil {
		return Result{name, Fail, err.Error(), "fix compose_provider in ~/.convergence/config.yaml"}
	}
	if err := e.Run(exec.CommandContext(ctx, bin, "info")); err != nil {
		fix := "start the Docker daemon"
		if bin == "podman" {
			fix = "check 'podman info' and rootless storage/session configuration"
		}
		return Result{name, Fail, fmt.Sprintf("'%s info' failed: %v", bin, err), fix}
	}
	provider := e.Cfg.ComposeProvider
	if err := e.Run(engine.ComposeCommand(ctx, bin, provider, "version")); err != nil {
		fix := "install the compose plugin (docker-compose-plugin or podman-compose)"
		if provider != "" && provider != "auto" {
			fix = "install " + provider + " on PATH, or set compose_provider: auto"
		}
		return Result{name, Fail, fmt.Sprintf("'%s compose version' failed (provider=%s): %v", bin, orDefault(provider, "auto"), err), fix}
	}
	return Result{name, Pass, bin + " reachable, compose available (provider=" + orDefault(provider, "auto") + ")", ""}
}

// CheckServices reports each service's running/healthy state via the health package.
func CheckServices(ctx context.Context, e *Env) []Result {
	if e.Services == nil {
		return []Result{{"services", Warn, "no service health checker wired", "update convergence"}}
	}
	sts := e.Services.Check(ctx)
	if len(sts) == 0 {
		return []Result{{"services", Fail, "no services reported", "run 'convergence start'"}}
	}
	out := make([]Result, 0, len(sts))
	for _, s := range sts {
		n := "service " + s.Name
		switch {
		case !s.Running:
			out = append(out, Result{n, Fail, orDefault(s.Detail, "not running"), "run 'convergence start' then 'convergence logs " + s.Name + "'"})
		case !s.Healthy:
			out = append(out, Result{n, Fail, orDefault(s.Detail, "running but unhealthy"), "inspect with 'convergence logs " + s.Name + "'"})
		default:
			out = append(out, Result{n, Pass, orDefault(s.Detail, "running and healthy"), ""})
		}
	}
	return out
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// CheckPorts verifies every published port accepts connections and that the
// bind address is loopback-only.
func CheckPorts(ctx context.Context, e *Env) []Result {
	var out []Result
	bind := e.Cfg.BindAddress
	if bind != "127.0.0.1" {
		out = append(out, Result{"bind address", Warn, fmt.Sprintf("bind_address=%q exposes the gateway beyond loopback", bind),
			"set bind_address: 127.0.0.1 in ~/.convergence/config.yaml and run 'convergence restart'"})
	} else {
		out = append(out, Result{"bind address", Pass, "published ports bind to 127.0.0.1 only", ""})
	}
	host := e.Cfg.HealthHost()
	for _, p := range e.Cfg.PublishedPorts() {
		n := fmt.Sprintf("port %d (%s)", p.Port, p.Name)
		if err := dial(ctx, portAddr(host, p.Port)); err != nil {
			out = append(out, Result{n, Fail, "not listening on " + portAddr(host, p.Port) + ": " + shortErr(err), "run 'convergence start'"})
			continue
		}
		out = append(out, Result{n, Pass, "listening on " + portAddr(host, p.Port), ""})
	}
	return out
}

// composeStandinMode reads STANDIN_MODE from the rendered compose file.
func composeStandinMode(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yaml"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(data), "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "STANDIN_MODE:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "STANDIN_MODE:")), `"'`)
		}
	}
	return ""
}

// standinFailOpen reports whether the rendered gateway config has Stand-In
// failing open. ok=false when it cannot be determined.
func standinFailOpen(dir string) (open, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, "agentgateway.yaml"))
	if err != nil {
		return false, false
	}
	var y struct {
		Gateways map[string]struct {
			ExtProc struct {
				Conditional []struct {
					Host        string `yaml:"host"`
					FailureMode string `yaml:"failureMode"`
				} `yaml:"conditional"`
			} `yaml:"extProc"`
		} `yaml:"gateways"`
	}
	if yaml.Unmarshal(data, &y) != nil {
		return false, false
	}
	for _, g := range y.Gateways {
		for _, c := range g.ExtProc.Conditional {
			if strings.HasPrefix(c.Host, "stand-in") {
				return c.FailureMode == "failOpen", true
			}
		}
	}
	return false, false
}

// CheckFailMode reports whether Stand-In is fail-closed or fail-open.
func CheckFailMode(e *Env) Result {
	const name = "stand-in fail mode"
	rendered, ok := standinFailOpen(e.Dir)
	hint := "set fail_open: false in ~/.convergence/config.yaml and run 'convergence restart' (fail-closed blocks traffic when Stand-In is down)"
	switch {
	case e.FailOpen || (ok && rendered):
		src := "config fail_open: true"
		if !e.FailOpen {
			src = "rendered agentgateway.yaml has failureMode: failOpen for stand-in (config fail_open is false/missing)"
		}
		return Result{name, Warn, "FAIL-OPEN: if Stand-In is down, requests reach the provider WITH raw secrets (" + src + ")", hint}
	default:
		return Result{name, Pass, "fail-closed: requests are blocked when Stand-In is unavailable", ""}
	}
}

// CheckSkills reports the cached SkillSpector verdict on installed skills. It
// never runs a scan: that takes a container start per call.
func CheckSkills(e *Env) Result {
	const name = "skills"
	hint := "run 'convergence skills scan'"
	if e.Cfg.SkillScanOrDefault() == config.SkillScanOff {
		return Result{name, Info, "skill_scan: off (sandbox run does not vet installed skills)", ""}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{name, Warn, err.Error(), ""}
	}
	dirs := skillscan.Discover(home)
	if len(dirs) == 0 {
		return Result{name, Info, "no installed skills found", ""}
	}
	rep, err := skillscan.Load(filepath.Join(e.Dir, skillscan.LastScanFile))
	switch {
	case err != nil:
		return Result{name, Warn, err.Error(), hint}
	case rep == nil:
		return Result{name, Warn, fmt.Sprintf("%d installed skill(s) never scanned", len(dirs)), hint}
	case rep.Fingerprint != skillscan.Fingerprint(dirs):
		return Result{name, Warn, fmt.Sprintf("installed skills changed since the scan of %s", rep.ScannedAt.Format("2006-01-02 15:04")), hint}
	}
	if bad := rep.Unsafe(); len(bad) > 0 {
		names := make([]string, len(bad))
		for i, s := range bad {
			names[i] = s.Name
		}
		st := Warn
		if e.Cfg.SkillScanOrDefault() == config.SkillScanBlock {
			st = Fail
		}
		return Result{name, st, "DO_NOT_INSTALL: " + strings.Join(names, ", "), "review or remove them; details: convergence skills scan"}
	}
	return Result{name, Pass, fmt.Sprintf("%d skill(s) scanned, none DO_NOT_INSTALL", len(rep.Skills)), ""}
}
