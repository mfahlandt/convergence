package clientconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Format int

const (
	JSON Format = iota
	YAML
)

const Key = "convergence"

// Options control a configure/restore run. GatewayURL is the gateway root
// (e.g. http://localhost:3000); a trailing /v1 is tolerated and removed.
type Options struct {
	GatewayURL string
	SetDefault bool
	DryRun     bool
	Force      bool
}

type setOp struct {
	path          []string
	val           any
	onlyIfMissing bool
}

type client struct {
	name    string
	format  Format
	path    func(home string) string
	ops     func(o Options, root string) []setOp
	pointed func(doc map[string]any) bool
	note    string
}

func Root(u string) string {
	return strings.TrimSuffix(strings.TrimRight(u, "/"), "/v1")
}

func home() (string, error) { return os.UserHomeDir() }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// HermesHome mirrors Hermes' own lookup: $HERMES_HOME, else %LOCALAPPDATA%\hermes
// on Windows, else ~/.hermes.
func HermesHome(h string) string {
	if d := strings.TrimSpace(os.Getenv("HERMES_HOME")); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		if d := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); d != "" {
			return filepath.Join(d, "hermes")
		}
		return filepath.Join(h, "AppData", "Local", "hermes")
	}
	return filepath.Join(h, ".hermes")
}

var clients = []client{
	{
		name:   "opencode",
		format: JSON,
		path: func(h string) string {
			d := filepath.Join(h, ".config", "opencode")
			if !exists(filepath.Join(d, "opencode.json")) && exists(filepath.Join(d, "opencode.jsonc")) {
				return filepath.Join(d, "opencode.jsonc")
			}
			return filepath.Join(d, "opencode.json")
		},
		ops: func(o Options, root string) []setOp {
			ops := []setOp{
				{path: []string{"$schema"}, val: "https://opencode.ai/config.json", onlyIfMissing: true},
				{path: []string{"provider", Key}, val: map[string]any{
					"npm":     "@ai-sdk/openai-compatible",
					"name":    "Convergence",
					"options": map[string]any{"baseURL": root + "/v1", "apiKey": Key},
					"models": map[string]any{
						"auto":     map[string]any{"name": "Convergence Auto"},
						"pinball":  map[string]any{"name": "Convergence Pinball"},
						"light":    map[string]any{"name": "Convergence Light"},
						"standard": map[string]any{"name": "Convergence Standard"},
						"heavy":    map[string]any{"name": "Convergence Heavy"},
					},
				}},
			}
			if o.SetDefault {
				ops = append(ops, setOp{path: []string{"model"}, val: Key + "/auto"})
			}
			return ops
		},
		pointed: func(d map[string]any) bool { _, ok := lookup(d, "provider", Key); return ok },
		note:    "Models: convergence/auto, convergence/pinball, convergence/light, convergence/standard, convergence/heavy. Default model untouched unless --set-default.",
	},
	{
		name:   "openclaw",
		format: JSON,
		path:   func(h string) string { return filepath.Join(h, ".openclaw", "openclaw.json") },
		ops: func(o Options, root string) []setOp {
			ops := []setOp{{path: []string{"models", "providers", Key}, val: map[string]any{
				"baseUrl": root + "/v1",
				"apiKey":  Key,
				"api":     "openai-completions",
				"models": []any{
					map[string]any{"id": "auto", "name": "Convergence Auto"},
					map[string]any{"id": "light", "name": "Convergence Light"},
					map[string]any{"id": "standard", "name": "Convergence Standard"},
					map[string]any{"id": "heavy", "name": "Convergence Heavy"},
				},
			}}}
			if o.SetDefault {
				ops = append(ops, setOp{path: []string{"agents", "defaults", "model", "primary"}, val: Key + "/auto"})
			}
			return ops
		},
		pointed: func(d map[string]any) bool { _, ok := lookup(d, "models", "providers", Key); return ok },
		// shortcut: strict JSON/JSONC only, an openclaw.json using JSON5 syntax (unquoted keys, single quotes) is rejected, not rewritten.
		note: "Models: convergence/auto, convergence/light, convergence/standard, convergence/heavy. Default model untouched unless --set-default. openclaw.json must be JSON/JSONC (no unquoted keys or single quotes).",
	},
	{
		name:   "claude-code",
		format: JSON,
		path:   func(h string) string { return filepath.Join(h, ".claude", "settings.json") },
		ops: func(_ Options, root string) []setOp {
			// Claude Code appends /v1/messages itself: base URL must not end in /v1.
			return []setOp{
				{path: []string{"env", "ANTHROPIC_BASE_URL"}, val: root},
				{path: []string{"env", "ANTHROPIC_AUTH_TOKEN"}, val: Key},
			}
		},
		pointed: func(d map[string]any) bool { return lookupString(d, "env", "ANTHROPIC_AUTH_TOKEN") == Key },
		note:    "Uses ANTHROPIC_AUTH_TOKEN (Authorization: Bearer). An existing ANTHROPIC_API_KEY in env is left alone.",
	},
	{
		name:   "aider",
		format: YAML,
		path:   func(h string) string { return filepath.Join(h, ".aider.conf.yml") },
		ops: func(_ Options, root string) []setOp {
			return []setOp{
				{path: []string{"openai-api-base"}, val: root + "/v1"},
				{path: []string{"openai-api-key"}, val: Key},
				{path: []string{"model"}, val: "openai/auto"},
			}
		},
		pointed: func(d map[string]any) bool { return lookupString(d, "openai-api-key") == Key },
		note:    "Configures openai-api-base and model openai/auto. Note: export OPENAI_API_BASE=" + Key + " if using LiteLLM env mode.",
	},
	{
		name:   "goose",
		format: YAML,
		path:   func(h string) string { return filepath.Join(h, ".config", "goose", "config.yaml") },
		ops: func(_ Options, root string) []setOp {
			return []setOp{
				{path: []string{"GOOSE_PROVIDER"}, val: "openai"},
				{path: []string{"GOOSE_MODEL"}, val: "auto"},
				{path: []string{"OPENAI_HOST"}, val: root},
				{path: []string{"OPENAI_BASE_PATH"}, val: "v1/chat/completions"},
			}
		},
		pointed: func(d map[string]any) bool {
			return lookupString(d, "OPENAI_BASE_PATH") != "" && lookupString(d, "OPENAI_HOST") != ""
		},
		note: "Goose does not read API keys from config.yaml: export OPENAI_API_KEY=convergence (or store it via `goose configure`).",
	},
	{
		name:   "hermes",
		format: YAML,
		path:   func(h string) string { return filepath.Join(HermesHome(h), "config.yaml") },
		ops: func(o Options, root string) []setOp {
			ops := []setOp{{path: []string{"providers", Key}, val: map[string]any{
				"name":     "Convergence",
				"base_url": root + "/v1",
				"api_key":  Key,
				"models": map[string]any{
					"auto":     map[string]any{},
					"pinball":  map[string]any{},
					"light":    map[string]any{},
					"standard": map[string]any{},
					"heavy":    map[string]any{},
				},
			}}}
			if o.SetDefault {
				// model.base_url is set too: a stale one from another provider would win.
				ops = append(ops,
					setOp{path: []string{"model", "provider"}, val: Key},
					setOp{path: []string{"model", "default"}, val: "auto"},
					setOp{path: []string{"model", "base_url"}, val: root + "/v1"})
			}
			return ops
		},
		pointed: func(d map[string]any) bool { _, ok := lookup(d, "providers", Key); return ok },
		note:    "Select it with `/model convergence:auto`; model.default/provider are untouched unless --set-default.",
	},
	{
		name:   "continue",
		format: YAML,
		path:   func(h string) string { return filepath.Join(h, ".continue", "config.yaml") },
		ops: func(_ Options, root string) []setOp {
			return []setOp{
				{path: []string{"name"}, val: "Local Config", onlyIfMissing: true},
				{path: []string{"version"}, val: "1.0.0", onlyIfMissing: true},
				{path: []string{"schema"}, val: "v1", onlyIfMissing: true},
				{path: []string{"models", itemPrefix + "Convergence (Auto)"}, val: map[string]any{
					"name": "Convergence (Auto)", "provider": "openai", "model": "auto",
					"apiBase": root + "/v1", "apiKey": Key,
				}},
				{path: []string{"models", itemPrefix + "Convergence (Light)"}, val: map[string]any{
					"name": "Convergence (Light)", "provider": "openai", "model": "light",
					"apiBase": root + "/v1", "apiKey": Key,
				}},
				{path: []string{"models", itemPrefix + "Convergence (Standard)"}, val: map[string]any{
					"name": "Convergence (Standard)", "provider": "openai", "model": "standard",
					"apiBase": root + "/v1", "apiKey": Key,
				}},
				{path: []string{"models", itemPrefix + "Convergence (Heavy)"}, val: map[string]any{
					"name": "Convergence (Heavy)", "provider": "openai", "model": "heavy",
					"apiBase": root + "/v1", "apiKey": Key,
				}},
			}
		},
		pointed: func(d map[string]any) bool {
			_, ok := lookup(d, "models", itemPrefix+"Convergence (Auto)")
			return ok
		},
		note: "Targets config.yaml (config.json is deprecated upstream).",
	},
	{
		name:   "zed",
		format: JSON,
		path: func(h string) string {
			return filepath.Join(h, ".config", "zed", "settings.json")
		},
		ops: func(_ Options, root string) []setOp {
			return []setOp{
				{path: []string{"language_models", "openai", "api_url"}, val: root + "/v1"},
				{path: []string{"language_models", "openai", "available_models"}, val: []any{
					map[string]any{"name": "auto", "display_name": "Convergence Auto", "max_tokens": 128000},
					map[string]any{"name": "light", "display_name": "Convergence Light", "max_tokens": 128000},
					map[string]any{"name": "standard", "display_name": "Convergence Standard", "max_tokens": 128000},
					map[string]any{"name": "heavy", "display_name": "Convergence Heavy", "max_tokens": 128000},
				}},
			}
		},
		pointed: func(d map[string]any) bool {
			u := lookupString(d, "language_models", "openai", "api_url")
			return strings.Contains(u, "3000") || strings.Contains(u, "/v1")
		},
		note: "Configures language_models.openai.api_url in ~/.config/zed/settings.json. Set OPENAI_API_KEY=convergence in your environment.",
	},
	{
		name:   "cline",
		format: JSON,
		path: func(h string) string {
			return filepath.Join(h, ".config", "Code", "User", "settings.json")
		},
		ops: func(_ Options, root string) []setOp {
			return []setOp{
				{path: []string{"cline.apiProvider"}, val: "openai-compatible"},
				{path: []string{"cline.openAiBaseUrl"}, val: root + "/v1"},
				{path: []string{"cline.openAiApiKey"}, val: Key},
				{path: []string{"cline.openAiModelId"}, val: "auto"},
			}
		},
		pointed: func(d map[string]any) bool {
			return lookupString(d, "cline.openAiApiKey") == Key
		},
		note: "Configures VS Code User settings for Cline / Roo Code with provider openai-compatible and model auto.",
	},
}

func init() {
	oc := clients[0]
	oc.name = "oh-my-opencode"
	clients = append(clients, oc)
}

func Names() []string {
	n := make([]string, len(clients))
	for i, c := range clients {
		n[i] = c.name
	}
	return n
}

func find(name string) (*client, string, error) {
	h, err := home()
	if err != nil {
		return nil, "", err
	}
	for i := range clients {
		if clients[i].name == name {
			return &clients[i], clients[i].path(h), nil
		}
	}
	return nil, "", fmt.Errorf("unknown client %q (supported: %s)", name, strings.Join(Names(), ", "))
}

// EnvExports prints shell exports for tools without a config file we can safely edit.
func EnvExports(gatewayURL string) string {
	r := Root(gatewayURL)
	return "# Add to your shell profile:\n" +
		"export OPENAI_BASE_URL=\"" + r + "/v1\"\n" +
		"export OPENAI_API_KEY=\"" + Key + "\"\n" +
		"export ANTHROPIC_BASE_URL=\"" + r + "\"\n" +
		"export ANTHROPIC_AUTH_TOKEN=\"" + Key + "\"\n"
}
