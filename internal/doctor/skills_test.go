package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jeefy/convergence/internal/config"
	"github.com/jeefy/convergence/internal/skillscan"
)

func TestCheckSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	e := newEnv(t)
	if r := CheckSkills(e); r.Status != Info {
		t.Fatalf("off by default: %+v", r)
	}
	e.Cfg.SkillScan = config.SkillScanWarn
	if r := CheckSkills(e); r.Status != Info {
		t.Fatalf("no skills: %+v", r)
	}
	d := filepath.Join(home, ".claude", "skills", "evil")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := CheckSkills(e); r.Status != Warn {
		t.Fatalf("never scanned: %+v", r)
	}
	rep := &skillscan.Report{Fingerprint: skillscan.Fingerprint([]string{d}), Skills: []skillscan.Skill{{Name: "evil", Recommendation: skillscan.RecommendBlock}}}
	if err := skillscan.Save(filepath.Join(e.Dir, skillscan.LastScanFile), rep); err != nil {
		t.Fatal(err)
	}
	if r := CheckSkills(e); r.Status != Warn {
		t.Errorf("unsafe in warn mode must not fail doctor: %+v", r)
	}
	e.Cfg.SkillScan = config.SkillScanBlock
	if r := CheckSkills(e); r.Status != Fail {
		t.Errorf("unsafe in block mode: %+v", r)
	}
	rep.Skills[0].Recommendation = "SAFE"
	if err := skillscan.Save(filepath.Join(e.Dir, skillscan.LastScanFile), rep); err != nil {
		t.Fatal(err)
	}
	if r := CheckSkills(e); r.Status != Pass {
		t.Errorf("safe: %+v", r)
	}
	if err := os.WriteFile(filepath.Join(d, "new.sh"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := CheckSkills(e); r.Status != Warn {
		t.Errorf("changed skills: %+v", r)
	}
	e.Cfg.SkillScan = config.SkillScanOff
	if r := CheckSkills(e); r.Status != Info {
		t.Errorf("off: %+v", r)
	}
}
