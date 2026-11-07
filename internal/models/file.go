package models

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jeefy/convergence/internal/config"
)

// FileName is the optional user override file inside the config dir.
const FileName = "models.yaml"

// ParseOverrides parses models.yaml content (strict: unknown shapes error).
func ParseOverrides(data []byte) (Overrides, error) {
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing models.yaml: %w", err)
	}
	delete(raw, "pinball") // handled by ParsePinball
	o := Overrides{}
	for k, n := range raw {
		var m map[string]string
		if err := n.Decode(&m); err != nil {
			return nil, fmt.Errorf("parsing models.yaml: %w", err)
		}
		o[k] = m
	}
	if err := validateOverrides(o); err != nil {
		return nil, err
	}
	return o, nil
}

// LoadOverrides reads dir/models.yaml; a missing file yields empty overrides.
func LoadOverrides(dir string) (Overrides, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return Overrides{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading models.yaml: %w", err)
	}
	return ParseOverrides(data)
}

func loadPinball(dir string) (*Pinball, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading models.yaml: %w", err)
	}
	return ParsePinball(data)
}

// ForConfig resolves the active mapping: defaults < config ollama flags < dir/models.yaml.
func ForConfig(cfg *config.Config, dir string) (*Resolved, error) {
	file, err := LoadOverrides(dir)
	if err != nil {
		return nil, err
	}
	o := Merge(OverridesFromConfig(cfg), file)
	res, err := Resolve(cfg.Provider, &o)
	if err != nil {
		return nil, err
	}
	pb, err := loadPinball(dir)
	if err != nil {
		return nil, err
	}
	return res, res.ApplyPinball(pb)
}

// StarterFile returns a commented models.yaml showing every default.
func StarterFile() string {
	var b strings.Builder
	b.WriteString("# convergence model overrides: provider -> pinball model -> upstream model.\n")
	b.WriteString("# Uncomment and edit only what you want to change; everything else uses the defaults.\n")
	b.WriteString("# direct: prefix with anthropic: (default) or openai:. Gemini is not supported in v0.1.\n")
	b.WriteString("# Check the result with: convergence models\n")
	provs := []config.ProviderType{config.ProviderCopilot, config.ProviderDirect, config.ProviderOllama}
	for _, p := range provs {
		fmt.Fprintf(&b, "#\n# %s:\n", p)
		names := make([]string, 0)
		for n := range defaults[p] {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&b, "#   %s: %s\n", n, defaults[p][n])
		}
	}
	return b.String()
}
