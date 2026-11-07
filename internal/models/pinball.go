package models

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Pinball is the optional `pinball:` block of models.yaml: overrides for
// pinball's thresholds and scoring (pinball 0.7.4 schema). Unset keys keep
// pinball's own defaults.
type Pinball struct {
	Thresholds struct {
		Standard *int `yaml:"standard"`
		Heavy    *int `yaml:"heavy"`
	} `yaml:"thresholds"`
	Scoring map[string]int `yaml:"scoring"`
}

// scoringDefaults are pinball's defaults; only used to validate cutoff order.
var scoringDefaults = map[string]int{
	"code": 1, "heavy_keyword": 1, "max_heavy_keywords": 2, "light_keyword": -1,
	"long_chars": 4000, "long": 1, "very_long_chars": 12000, "very_long": 2,
	"short_chars": 160, "short": -1, "tools": 1, "effort_high": 2, "effort_low": -1,
	"small_output_tokens": 512, "small_output": -1, "structured": -1,
}

// ParsePinball extracts the `pinball:` block from models.yaml content.
// Unknown keys inside it are errors.
func ParsePinball(data []byte) (*Pinball, error) {
	var f struct {
		Pinball yaml.Node `yaml:"pinball"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing models.yaml: %w", err)
	}
	if f.Pinball.IsZero() {
		return nil, nil
	}
	var p Pinball
	b, _ := yaml.Marshal(&f.Pinball)
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("models.yaml: pinball: %w", err)
	}
	var unknown []string
	for k := range p.Scoring {
		if _, ok := scoringDefaults[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("models.yaml: pinball.scoring: unknown key(s) %s", strings.Join(unknown, ", "))
	}
	return &p, nil
}

// ApplyPinball layers p (may be nil) onto r and validates the result.
func (r *Resolved) ApplyPinball(p *Pinball) error {
	if p == nil {
		return nil
	}
	if v := p.Thresholds.Standard; v != nil {
		r.Thresholds.Standard = *v
	}
	if v := p.Thresholds.Heavy; v != nil {
		r.Thresholds.Heavy = *v
	}
	if r.Thresholds.Heavy <= r.Thresholds.Standard {
		return fmt.Errorf("models.yaml: pinball.thresholds.heavy (%d) must be greater than standard (%d)", r.Thresholds.Heavy, r.Thresholds.Standard)
	}
	eff := func(k string) int {
		if v, ok := p.Scoring[k]; ok {
			return v
		}
		return scoringDefaults[k]
	}
	for _, k := range []string{"long_chars", "very_long_chars"} {
		if eff(k) <= 0 {
			return fmt.Errorf("models.yaml: pinball.scoring.%s must be greater than 0", k)
		}
	}
	if eff("very_long_chars") <= eff("long_chars") {
		return fmt.Errorf("models.yaml: pinball.scoring.very_long_chars must be greater than long_chars")
	}
	for _, k := range []string{"max_heavy_keywords", "short_chars", "small_output_tokens"} {
		if eff(k) < 0 {
			return fmt.Errorf("models.yaml: pinball.scoring.%s must not be negative", k)
		}
	}
	r.Scoring = p.Scoring
	return nil
}

// ScoringLines renders the scoring overrides as sorted "key: value" lines
// (empty when none), for the templates to indent.
func (r *Resolved) ScoringLines() []string {
	keys := make([]string, 0, len(r.Scoring))
	for k := range r.Scoring {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s: %d", k, r.Scoring[k]))
	}
	return out
}
