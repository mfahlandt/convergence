// Package models is the single source of truth for which pinball model names
// exist, which tier each belongs to, and which upstream model serves each one
// per provider. Both pinball.yaml and the agentgateway virtualModels are
// generated from Resolve's output (local templates and the k8s installer).
//
// This package must not import internal/templates or internal/k8s.
package models

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jeefy/convergence/internal/config"
)

// Tier is a pinball routing tier.
type Tier string

const (
	TierLight    Tier = "light"
	TierStandard Tier = "standard"
	TierHeavy    Tier = "heavy"
)

// Upstream provider identifiers (agentgateway provider families).
const (
	UpstreamCopilot   = "copilot"
	UpstreamOpenAI    = "openai"
	UpstreamAnthropic = "anthropic"
	UpstreamOllama    = "ollama"
)

// Model is one routable pinball model.
type Model struct {
	PinballName      string // key in pinball.yaml `models:` and tiers
	Tier             Tier
	UpstreamProvider string // UpstreamCopilot | UpstreamOpenAI | UpstreamAnthropic | UpstreamOllama
	UpstreamModel    string // model id sent upstream
	PromptTokens     int    // pinball cost cap for this model
}

// Resolved is the full routing model for one provider.
type Resolved struct {
	Provider   config.ProviderType
	Models     []Model // light, standard, heavy order
	Tiers      map[Tier][]string
	Thresholds Thresholds
	// Scoring holds optional pinball scoring overrides (models.yaml `pinball.scoring`).
	Scoring map[string]int
	// Fallback is the PinballName used when pinball is unreachable.
	Fallback string
}

// Thresholds are pinball's score cutoffs (pinball 0.7.4 schema).
type Thresholds struct{ Standard, Heavy int }

// Overrides represents models.yaml configuration.
type Overrides map[string]map[string]string

// MissingMappingError lists pinball models with no upstream mapping.
type MissingMappingError struct {
	Provider config.ProviderType
	Missing  []string
}

func (e *MissingMappingError) Error() string {
	return fmt.Sprintf("provider %s has no upstream mapping for pinball model(s): %s (add them to ~/.convergence/models.yaml)",
		e.Provider, strings.Join(e.Missing, ", "))
}

type slot struct {
	name   string
	tier   Tier
	tokens int
}

// The pinball pool: one model per tier. Names are labels shared with
// pinball's own catalog (github.com/jeefy/pinball); the provider tables below
// say what actually serves them.
var pool = []slot{
	{"claude-haiku-5.5", TierLight, 100000},
	{"claude-sonnet-5.5", TierStandard, 200000},
	{"claude-opus-5.5", TierHeavy, 200000},
}

const ollamaPromptTokens = 32000 // local runtimes: conservative context

// Defaults per provider: pinball name -> "[family:]model".
var defaults = map[config.ProviderType]map[string]string{
	// Copilot ids are the ids copilot-api serves for a Copilot subscription,
	// as used by the production carrier gateway (k8s/agentgateway.yaml).
	config.ProviderCopilot: {
		"claude-haiku-5.5":  "claude-haiku-5.5",
		"claude-sonnet-5.5": "claude-sonnet-5.5",
		"claude-opus-5.5":   "claude-opus-5.5",
	},
	// Anthropic API ids (docs.anthropic.com models overview: dashes, not dots).
	config.ProviderDirect: {
		"claude-haiku-5.5":  "anthropic:claude-haiku-5-5",
		"claude-sonnet-5.5": "anthropic:claude-sonnet-5-5",
		"claude-opus-5.5":   "anthropic:claude-opus-5-5",
	},
	// Local defaults (all exist in the ollama library). Override with
	// models.yaml or `init --ollama-model-light/-standard/-heavy`.
	config.ProviderOllama: {
		"claude-haiku-5.5":  "qwen2.5-coder:7b",
		"claude-sonnet-5.5": "qwen2.5-coder:14b",
		"claude-opus-5.5":   "qwen2.5-coder:32b",
	},
}

// OverridesFromConfig turns the init-time per-tier ollama flags into overrides.
func OverridesFromConfig(cfg *config.Config) Overrides {
	o := Overrides{}
	set := func(name, v string) {
		if v == "" {
			return
		}
		if o["ollama"] == nil {
			o["ollama"] = map[string]string{}
		}
		o["ollama"][name] = v
	}
	set("claude-haiku-5.5", cfg.OllamaModelLight)
	set("claude-sonnet-5.5", cfg.OllamaModelStandard)
	set("claude-opus-5.5", cfg.OllamaModelHeavy)
	return o
}

// Merge returns a with b's entries layered on top (b wins). Nil-safe.
func Merge(a, b Overrides) Overrides {
	out := Overrides{}
	for _, src := range []Overrides{a, b} {
		for p, m := range src {
			if out[p] == nil {
				out[p] = map[string]string{}
			}
			for k, v := range m {
				out[p][k] = v
			}
		}
	}
	return out
}

// Resolve builds the routing model for provider, applying override (may be nil).
func Resolve(provider config.ProviderType, override *Overrides) (*Resolved, error) {
	table, ok := defaults[provider]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q (want copilot, direct or ollama)", provider)
	}
	merged := map[string]string{}
	for k, v := range table {
		merged[k] = v
	}
	if override != nil {
		if err := validateOverrides(*override); err != nil {
			return nil, err
		}
		for k, v := range (*override)[string(provider)] {
			merged[k] = v
		}
	}
	return build(provider, pool, merged)
}

func build(provider config.ProviderType, slots []slot, mapping map[string]string) (*Resolved, error) {
	var missing []string
	r := &Resolved{
		Provider:   provider,
		Tiers:      map[Tier][]string{},
		Thresholds: Thresholds{Standard: 1, Heavy: 3},
	}
	for _, s := range slots {
		spec := strings.TrimSpace(mapping[s.name])
		if spec == "" {
			missing = append(missing, s.name)
			continue
		}
		family, model, err := splitSpec(provider, spec)
		if err != nil {
			return nil, fmt.Errorf("models.yaml %s/%s: %w", provider, s.name, err)
		}
		tokens := s.tokens
		if provider == config.ProviderOllama {
			tokens = ollamaPromptTokens
		}
		r.Models = append(r.Models, Model{s.name, s.tier, family, model, tokens})
		r.Tiers[s.tier] = append(r.Tiers[s.tier], s.name)
		if s.tier == TierStandard && r.Fallback == "" {
			r.Fallback = s.name
		}
	}
	if len(missing) > 0 {
		return nil, &MissingMappingError{Provider: provider, Missing: missing}
	}
	return r, nil
}

func splitSpec(provider config.ProviderType, spec string) (family, model string, err error) {
	fam, m, found := strings.Cut(spec, ":")
	if found {
		switch fam {
		case "anthropic":
			return UpstreamAnthropic, m, nil
		case "openai":
			return UpstreamOpenAI, m, nil
		case "ollama":
			return UpstreamOllama, m, nil
		case "copilot":
			return UpstreamCopilot, m, nil
		}
	}

	switch provider {
	case config.ProviderCopilot:
		return UpstreamCopilot, spec, nil
	case config.ProviderOllama:
		return UpstreamOllama, spec, nil
	case config.ProviderDirect:
		if found {
			return "", "", fmt.Errorf("unsupported upstream family %q (supports anthropic:, openai:, ollama:, copilot:)", fam)
		}
		return UpstreamAnthropic, spec, nil
	}
	return "", "", fmt.Errorf("unknown provider %q", provider)
}

func validateOverrides(o Overrides) error {
	known := map[string]bool{}
	for _, s := range pool {
		known[s.name] = true
	}
	var names []string
	for _, s := range pool {
		names = append(names, s.name)
	}
	provs := make([]string, 0, len(o))
	for p := range o {
		provs = append(provs, p)
	}
	sort.Strings(provs)
	for _, p := range provs {
		if _, err := config.ParseProvider(p); err != nil {
			return fmt.Errorf("models.yaml: %w (Gemini and other providers are not supported in v0.1)", err)
		}
		for name, v := range o[p] {
			if !known[name] {
				return fmt.Errorf("models.yaml: %s: unknown pinball model %q (known: %s)", p, name, strings.Join(names, ", "))
			}
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("models.yaml: %s: empty upstream model for %q", p, name)
			}
			provType, err := config.ParseProvider(p)
			if err != nil {
				return fmt.Errorf("models.yaml: %w", err)
			}
			if _, _, err := splitSpec(provType, v); err != nil {
				return fmt.Errorf("models.yaml: %s/%s: %w", p, name, err)
			}
		}
	}
	return nil
}

// UsesFamily reports whether any model is served by the given upstream family.
func (r *Resolved) UsesFamily(family string) bool {
	for _, m := range r.Models {
		if m.UpstreamProvider == family {
			return true
		}
	}
	return false
}

func (r *Resolved) TierModel(t string) string {
	models := r.Tiers[Tier(t)]
	if len(models) == 0 {
		return ""
	}
	return models[0]
}

// TierList renders a tier's pinball names as "a, b" for a YAML flow list.
func (r *Resolved) TierList(t string) string {
	return strings.Join(r.Tiers[Tier(t)], ", ")
}
