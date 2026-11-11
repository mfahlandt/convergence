package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeefy/convergence/internal/config"
	"github.com/jeefy/convergence/internal/skillscan"
)

// skillHome is a temp HOME with one installed skill and an optional cached scan.
func skillHome(t *testing.T, cached *skillscan.Report) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir()) // no engine: a rescan must not run
	d := filepath.Join(home, ".claude", "skills", "evil")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cached == nil {
		return
	}
	cached.Fingerprint = skillscan.Fingerprint([]string{d})
	if err := os.MkdirAll(filepath.Join(home, ".convergence"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := skillscan.Save(filepath.Join(home, ".convergence", skillscan.LastScanFile), cached); err != nil {
		t.Fatal(err)
	}
}

func unsafeReport() *skillscan.Report {
	return &skillscan.Report{ScannedAt: time.Now(), Skills: []skillscan.Skill{{Name: "evil", Recommendation: skillscan.RecommendBlock}}}
}

func TestPreflightSkills(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		mode    config.SkillScanMode
		cached  *skillscan.Report
		wantErr bool
		wantOut string
	}{
		{"off is default", "", unsafeReport(), false, ""},
		{"warn does not block", config.SkillScanWarn, unsafeReport(), false, "DO_NOT_INSTALL"},
		{"block stops the run", config.SkillScanBlock, unsafeReport(), true, ""},
		{"off skips", config.SkillScanOff, unsafeReport(), false, ""},
		{"safe cache passes", config.SkillScanBlock, &skillscan.Report{Skills: []skillscan.Skill{{Name: "evil", Recommendation: "SAFE"}}}, false, ""},
		{"no engine warns", config.SkillScanWarn, nil, false, "skill scan skipped"},
		{"no engine blocks in block mode", config.SkillScanBlock, nil, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skillHome(t, tc.cached)
			cfg := config.DefaultConfig()
			cfg.SkillScan = tc.mode
			var out bytes.Buffer
			err := preflightSkills(ctx, cfg, &out)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Errorf("output %q misses %q", out.String(), tc.wantOut)
			}
			if tc.mode != config.SkillScanWarn && tc.mode != config.SkillScanBlock && out.Len() > 0 {
				t.Errorf("off must be silent, got %q", out.String())
			}
		})
	}
}

func TestScanLLMNeedsModel(t *testing.T) {
	cfg := config.DefaultConfig()
	if _, err := scanLLM(cfg, ""); err == nil {
		t.Error("--llm without a model must fail")
	}
	l, err := scanLLM(cfg, "glm")
	if err != nil || l.BaseURL != "http://host.docker.internal:3000/v1" || l.Model != "glm" {
		t.Errorf("got %+v %v", l, err)
	}
}
