package templates

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jeefy/convergence/internal/config"
)

const (
	salt         = "salt-0123456789abcdef"
	copilotTok   = "gho_secretcopilot"
	openAIKey    = "sk-secret-openai"
	anthropicKey = "sk-ant-secret"
)

func render(t *testing.T, mutate func(*config.Config)) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.StandinSalt = salt
	cfg.CopilotToken = copilotTok
	cfg.OpenAIKey = openAIKey
	cfg.AnthropicKey = anthropicKey
	mutate(cfg)
	if err := RenderAssets(cfg, dir); err != nil {
		t.Fatalf("RenderAssets: %v", err)
	}
	files := map[string]string{}
	for _, f := range append(publicFiles, EnvFile) {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		files[f] = string(b)
	}
	return dir, files
}

func TestRenderAllProvidersAndEngines(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		for _, prov := range []config.ProviderType{config.ProviderCopilot, config.ProviderDirect, config.ProviderOllama} {
			t.Run(engine+"/"+string(prov), func(t *testing.T) {
				dir, files := render(t, func(c *config.Config) { c.Engine = engine; c.Provider = prov })

				for _, f := range publicFiles {
					for _, secret := range []string{salt, copilotTok, openAIKey, anthropicKey} {
						if strings.Contains(files[f], secret) {
							t.Errorf("%s leaks a secret", f)
						}
					}
					assertPerm(t, filepath.Join(dir, f), 0o644)
				}
				assertPerm(t, filepath.Join(dir, EnvFile), 0o600)
				assertPerm(t, dir, 0o755)

				compose, env := files[ComposeFile], files[EnvFile]
				if !strings.Contains(env, "STANDIN_SALT='"+salt+"'") {
					t.Errorf(".env missing salt")
				}
				hasCopilot := strings.Contains(compose, "copilot-api:")
				if hasCopilot && !strings.Contains(compose, "./copilot-api-bunfig.toml:/app/bunfig.toml:ro") {
					t.Errorf("copilot-api must preload the idle-timeout shim")
				}
				if hasCopilot != (prov == config.ProviderCopilot) {
					t.Errorf("copilot-api service present=%v for provider %s", hasCopilot, prov)
				}
				if (prov == config.ProviderCopilot) != strings.Contains(env, "GH_TOKEN='"+copilotTok+"'") {
					t.Errorf(".env GH_TOKEN presence wrong for %s", prov)
				}
				if (prov == config.ProviderDirect) != strings.Contains(env, "OPENAI_API_KEY='"+openAIKey+"'") {
					t.Errorf(".env OPENAI_API_KEY presence wrong for %s", prov)
				}
				if prov == config.ProviderDirect && !strings.Contains(files["agentgateway.yaml"], `apiKey: "$ANTHROPIC_API_KEY"`) {
					t.Errorf("agentgateway.yaml must reference keys via env")
				}
			})
		}
	}
}

func TestRenderOpenShellSandbox(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Sandbox = config.SandboxOpenShell
	if err := RenderAssets(cfg, dir); err != nil {
		t.Fatalf("RenderAssets with openshell: %v", err)
	}

	composeData, err := os.ReadFile(filepath.Join(dir, ComposeFile))
	if err != nil {
		t.Fatalf("read compose file: %v", err)
	}
	if !strings.Contains(string(composeData), "openshell:") {
		t.Errorf("expected openshell service in compose file")
	}

	tomlData, err := os.ReadFile(filepath.Join(dir, "openshell.toml"))
	if err != nil {
		t.Fatalf("read openshell.toml: %v", err)
	}
	if !strings.Contains(string(tomlData), "version = 2") {
		t.Errorf("expected schema version 2 in openshell.toml")
	}
}

func TestRenderBindsLoopbackByDefault(t *testing.T) {
	_, files := render(t, func(c *config.Config) { c.Provider = config.ProviderCopilot })
	for _, want := range []string{"127.0.0.1:3000:3000", "127.0.0.1:15020:15020", "127.0.0.1:9102:9102", "127.0.0.1:9201:9201", "127.0.0.1:4141:4141"} {
		if !strings.Contains(files[ComposeFile], `"`+want+`"`) {
			t.Errorf("compose missing port mapping %s", want)
		}
	}
}

func TestRenderHonorsBindAddressAndPort(t *testing.T) {
	_, files := render(t, func(c *config.Config) { c.BindAddress = "0.0.0.0"; c.GatewayPort = 8080 })
	if !strings.Contains(files[ComposeFile], `"0.0.0.0:8080:3000"`) {
		t.Errorf("compose missing 0.0.0.0:8080:3000")
	}
	if strings.Contains(files[ComposeFile], `"127.0.0.1:`) {
		t.Errorf("compose still binds loopback")
	}
}

func TestRenderTightensExistingPerms(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agentgateway.yaml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, EnvFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenderAssets(config.DefaultConfig(), dir); err != nil {
		t.Fatal(err)
	}
	assertPerm(t, dir, 0o755)
	assertPerm(t, filepath.Join(dir, "agentgateway.yaml"), 0o644)
	assertPerm(t, filepath.Join(dir, EnvFile), 0o600)
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s: perm %o, want %o", filepath.Base(path), got, want)
	}
}

// agentgateway expands $NAME anywhere in its config, comments included, and
// fails on unset names: only the provider key variables may appear.
func TestAgentgatewayOnlyReferencesProvidedEnv(t *testing.T) {
	_, files := render(t, func(c *config.Config) { c.Provider = config.ProviderDirect })
	allowed := map[string]bool{"$OPENAI_API_KEY": true, "$ANTHROPIC_API_KEY": true}
	for _, m := range regexp.MustCompile(`\$[A-Za-z_{][A-Za-z0-9_]*`).FindAllString(files["agentgateway.yaml"], -1) {
		if !allowed[m] {
			t.Errorf("unexpected env reference %s in agentgateway.yaml", m)
		}
	}
}

func TestFailClosedByDefaultAndFailOpenOptIn(t *testing.T) {
	_, files := render(t, func(c *config.Config) {})
	ag := files["agentgateway.yaml"]
	if !strings.Contains(ag, "failureMode: failClosed") || strings.Contains(ag, "failureMode: failOpen") {
		t.Errorf("default must be failClosed:\n%s", ag)
	}
	if !strings.Contains(ag, "failureMode: allow") {
		t.Errorf("pinball ext_authz must stay allow")
	}
	_, files = render(t, func(c *config.Config) { c.FailOpen = true })
	if !strings.Contains(files["agentgateway.yaml"], "failureMode: failOpen") {
		t.Errorf("fail_open must render failOpen")
	}
}

// Model names/ids come only from internal/models: a models.yaml override must
// show up in both generated files, and the defaults of other providers must not.
func TestTemplatesDrivenByModels(t *testing.T) {
	dir, _ := render(t, func(c *config.Config) { c.Provider = config.ProviderOllama })
	if err := os.WriteFile(filepath.Join(dir, "models.yaml"), []byte("ollama:\n  claude-haiku-5.5: tiny:1b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Provider = config.ProviderOllama
	if err := RenderAssets(cfg, dir); err != nil {
		t.Fatal(err)
	}
	ag, _ := os.ReadFile(filepath.Join(dir, "agentgateway.yaml"))
	pb, _ := os.ReadFile(filepath.Join(dir, "pinball.yaml"))
	if !strings.Contains(string(ag), "params: {model: tiny:1b}") {
		t.Errorf("override missing from agentgateway.yaml")
	}
	for _, f := range []string{string(ag), string(pb)} {
		for _, stale := range []string{"gemini", "gpt-4o", "claude-3", "llama3.2"} {
			if strings.Contains(f, stale) {
				t.Errorf("stale hardcoded name %q rendered", stale)
			}
		}
	}
	for _, want := range []string{"light: [claude-haiku-5.5]", "standard: [claude-sonnet-5.5]", "heavy: [claude-opus-5.5]", "standard: 1", "heavy: 3"} {
		if !strings.Contains(string(pb), want) {
			t.Errorf("pinball.yaml missing %q:\n%s", want, pb)
		}
	}
}

func TestMissingMappingFailsRender(t *testing.T) {
	cfg := config.DefaultConfig()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.yaml"), []byte("gemini:\n  x: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenderAssets(cfg, dir); err == nil {
		t.Fatal("gemini override must fail rendering")
	}
}

func TestComposeHasLoggingAndHealthDeps(t *testing.T) {
	_, files := render(t, func(c *config.Config) { c.Provider = config.ProviderCopilot })
	c := files[ComposeFile]
	if n := strings.Count(c, "max-size"); n != 4 {
		t.Errorf("logging on %d services, want 4", n)
	}
	if n := strings.Count(c, "condition: service_healthy"); n != 2 {
		t.Errorf("healthy deps %d, want 2", n)
	}
	if !strings.Contains(c, "stand-in:\n        condition: service_started") {
		t.Errorf("expected stand-in to have condition: service_started")
	}
}

// Some podman versions split healthcheck CMD arguments on whitespace, turning
// `python3 -c "import x; ..."` into a SyntaxError (seen on the CI runner:
// containers stayed unhealthy). The inline python must contain no spaces.
func TestHealthcheckPythonHasNoWhitespace(t *testing.T) {
	_, files := render(t, func(c *config.Config) { c.Provider = config.ProviderCopilot })
	re := regexp.MustCompile(`"python3", "-c", "([^"]*)"`)
	m := re.FindAllStringSubmatch(files[ComposeFile], -1)
	if len(m) != 1 {
		t.Fatalf("found %d python healthchecks, want 1", len(m))
	}
	for _, g := range m {
		if strings.ContainsAny(g[1], " \t;") {
			t.Errorf("healthcheck code must not contain whitespace or ';': %q", g[1])
		}
	}
}

func TestCustomImageOverrides(t *testing.T) {
	customAGW := "ghcr.io/agentgateway/agentgateway:sha-1234567"
	customStandIn := "ghcr.io/jeefy/stand-in:sha-abcdef"
	customPinball := "ghcr.io/jeefy/pinball:dev"
	customCopilot := "ghcr.io/ericc-ch/copilot-api:edge"

	_, files := render(t, func(c *config.Config) {
		c.Provider = config.ProviderCopilot
		c.ImageAgentGateway = customAGW
		c.ImageStandIn = customStandIn
		c.ImagePinball = customPinball
		c.ImageCopilotAPI = customCopilot
	})

	compose := files[ComposeFile]
	for _, expected := range []string{customAGW, customStandIn, customPinball, customCopilot} {
		if !strings.Contains(compose, expected) {
			t.Errorf("compose file does not contain overridden image %s", expected)
		}
	}
}

func TestAgentgatewayExposesPinballPickAsResponseHeaders(t *testing.T) {
	_, files := render(t, func(c *config.Config) { c.Provider = config.ProviderCopilot })
	for _, h := range []string{"x-pinball-model", "x-pinball-tier", "x-pinball-reason"} {
		if !strings.Contains(files["agentgateway.yaml"], h+`: 'default(extauthz.`) {
			t.Errorf("agentgateway config missing response header %s", h)
		}
	}
}
