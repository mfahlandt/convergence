package models

import (
	"errors"
	"strings"
	"testing"

	"github.com/jeefy/convergence/internal/config"
)

func TestResolve_defaultsForEveryProvider(t *testing.T) {
	want := map[config.ProviderType][3]string{
		config.ProviderCopilot: {"copilot/claude-haiku-5.5", "copilot/claude-sonnet-5.5", "copilot/claude-opus-5.5"},
		config.ProviderDirect:  {"anthropic/claude-haiku-5-5", "anthropic/claude-sonnet-5-5", "anthropic/claude-opus-5-5"},
		config.ProviderOllama:  {"ollama/qwen2.5-coder:7b", "ollama/qwen2.5-coder:14b", "ollama/qwen2.5-coder:32b"},
	}
	for p, w := range want {
		r, err := Resolve(p, nil)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(r.Models) != 3 {
			t.Fatalf("%s: %d models", p, len(r.Models))
		}
		for i, m := range r.Models {
			if got := m.UpstreamProvider + "/" + m.UpstreamModel; got != w[i] {
				t.Errorf("%s[%d] = %s, want %s", p, i, got, w[i])
			}
			if m.PromptTokens <= 0 {
				t.Errorf("%s: zero prompt tokens", m.PinballName)
			}
		}
		if r.Tiers[TierLight][0] != "claude-haiku-5.5" || r.Tiers[TierHeavy][0] != "claude-opus-5.5" || r.Fallback != "claude-sonnet-5.5" {
			t.Errorf("%s: bad tiers %v fallback %s", p, r.Tiers, r.Fallback)
		}
	}
}

func TestResolve_overrideWinsAndDirectFamilies(t *testing.T) {
	o := Overrides{
		"direct": {"claude-haiku-5.5": "openai:gpt-6-luna", "claude-opus-5.5": "ollama:deepseek-r1:70b"},
		"ollama": {"claude-opus-5.5": "llama3.3:70b", "claude-haiku-5.5": "openai:gpt-4o-mini"},
	}
	r, err := Resolve(config.ProviderDirect, &o)
	if err != nil {
		t.Fatal(err)
	}
	if m := r.Models[0]; m.UpstreamProvider != UpstreamOpenAI || m.UpstreamModel != "gpt-6-luna" {
		t.Errorf("override ignored: %+v", m)
	}
	if m := r.Models[2]; m.UpstreamProvider != UpstreamOllama || m.UpstreamModel != "deepseek-r1:70b" {
		t.Errorf("ollama mix in direct ignored: %+v", m)
	}
	if !r.UsesFamily("openai") || !r.UsesFamily("anthropic") || !r.UsesFamily("ollama") {
		t.Error("UsesFamily wrong")
	}
	r, _ = Resolve(config.ProviderOllama, &o)
	if r.Models[2].UpstreamModel != "llama3.3:70b" {
		t.Errorf("ollama override ignored: %+v", r.Models[2])
	}
	if r.Models[0].UpstreamProvider != UpstreamOpenAI {
		t.Errorf("openai mix in ollama ignored: %+v", r.Models[0])
	}
}

func TestResolve_rejectsBadOverrides(t *testing.T) {
	cases := map[string]Overrides{
		"gemini provider": {"gemini": {"claude-haiku-5.5": "x"}},
		"unknown model":   {"copilot": {"nope": "x"}},
		"gemini family":   {"direct": {"claude-haiku-5.5": "gemini:flash"}},
		"empty upstream":  {"copilot": {"claude-haiku-5.5": " "}},
	}
	for name, o := range cases {
		if _, err := Resolve(config.ProviderCopilot, &o); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestBuild_missingMappingNamesTheModels(t *testing.T) {
	_, err := build(config.ProviderCopilot, pool, map[string]string{"claude-haiku-5.5": "x"})
	var me *MissingMappingError
	if !errors.As(err, &me) || strings.Join(me.Missing, ",") != "claude-sonnet-5.5,claude-opus-5.5" {
		t.Fatalf("got %v", err)
	}
}

func TestFromConfigAndStarterRoundTrip(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Provider = config.ProviderOllama
	cfg.OllamaModelLight = "tiny:1b"
	r, err := ForConfig(cfg, t.TempDir())
	if err != nil || r.Models[0].UpstreamModel != "tiny:1b" {
		t.Fatalf("%v %+v", err, r)
	}
	// the starter file is all comments: parses to empty overrides
	o, err := ParseOverrides([]byte(StarterFile()))
	if err != nil || len(o) != 0 {
		t.Fatalf("starter: %v %v", err, o)
	}
}

func TestPinballBlock(t *testing.T) {
	good := []byte("copilot:\n  claude-haiku-5.5: x\npinball:\n  thresholds: {standard: 2}\n  scoring: {tools: 0, long_chars: 100}\n")
	if _, err := ParseOverrides(good); err != nil {
		t.Fatal(err)
	}
	p, err := ParsePinball(good)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := Resolve(config.ProviderCopilot, nil)
	if err := r.ApplyPinball(p); err != nil {
		t.Fatal(err)
	}
	if r.Thresholds.Standard != 2 || r.Thresholds.Heavy != 3 || r.Scoring["tools"] != 0 {
		t.Errorf("not applied: %+v %v", r.Thresholds, r.Scoring)
	}
	if got := strings.Join(r.ScoringLines(), ","); got != "long_chars: 100,tools: 0" {
		t.Errorf("lines %q", got)
	}
	if p, err := ParsePinball([]byte("copilot: {}\n")); p != nil || err != nil {
		t.Errorf("absent block: %v %v", p, err)
	}
	for name, y := range map[string]string{
		"unknown scoring": "pinball:\n  scoring: {nope: 1}\n",
		"unknown block":   "pinball:\n  extra: 1\n",
		"unknown thresh":  "pinball:\n  thresholds: {light: 1}\n",
		"typo":            "pinball:\n  scoring: {tools: x}\n",
	} {
		if _, err := ParsePinball([]byte(y)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	for name, y := range map[string]string{
		"order":     "pinball:\n  thresholds: {standard: 3}\n",
		"long":      "pinball:\n  scoring: {very_long_chars: 100}\n",
		"neg short": "pinball:\n  scoring: {short_chars: -1}\n",
	} {
		p, err := ParsePinball([]byte(y))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		r, _ := Resolve(config.ProviderCopilot, nil)
		if r.ApplyPinball(p) == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
