package skillscan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func mkSkill(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDiscover(t *testing.T) {
	home := t.TempDir()
	a := mkSkill(t, filepath.Join(home, ".claude/skills/a"))
	b := mkSkill(t, filepath.Join(home, ".hermes/skills/dev/b")) // Hermes category dir
	c := mkSkill(t, filepath.Join(home, ".config/opencode/skills/c"))
	if err := os.MkdirAll(filepath.Join(home, ".agents/skills/empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := Discover(home)
	for _, want := range []string{a, b, c} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %d dirs, want 3: %v", len(got), got)
	}
	if one := SkillDirs(a); !slices.Equal(one, []string{a}) {
		t.Errorf("a skill dir must return itself, got %v", one)
	}
}

func TestFingerprintChanges(t *testing.T) {
	d := mkSkill(t, filepath.Join(t.TempDir(), "s"))
	f1 := Fingerprint([]string{d})
	if f1 != Fingerprint([]string{d}) {
		t.Fatal("fingerprint not stable")
	}
	if err := os.WriteFile(filepath.Join(d, "run.sh"), []byte("curl x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f1 == Fingerprint([]string{d}) {
		t.Error("added file must change the fingerprint")
	}
}

func TestRunArgs(t *testing.T) {
	s := New("podman", "")
	static := strings.Join(s.RunArgs([]string{"/h/a", "/h/b"}, nil, ",z"), " ")
	for _, want := range []string{"--network=none", "-v /h/a:/scan/000-a:ro,z", "-v /h/b:/scan/001-b:ro,z", DefaultImage + " scan /scan --recursive --format json --no-llm"} {
		if !strings.Contains(static, want) {
			t.Errorf("static args miss %q: %s", want, static)
		}
	}
	llm := strings.Join(s.RunArgs([]string{"/h/a"}, &LLM{BaseURL: "http://host.docker.internal:3000/v1", Model: "glm"}, ""), " ")
	for _, want := range []string{"--add-host=host.docker.internal:host-gateway", "SKILLSPECTOR_COMPAT_BASE_URL=http://host.docker.internal:3000/v1", "SKILLSPECTOR_MODEL=glm", "SKILLSPECTOR_STRUCTURED_OUTPUT_METHOD=function_calling"} {
		if !strings.Contains(llm, want) {
			t.Errorf("llm args miss %q: %s", want, llm)
		}
	}
	if strings.Contains(llm, "--no-llm") || strings.Contains(llm, "--network=none") {
		t.Errorf("llm scan must not be offline: %s", llm)
	}
}

// Trimmed from a real SkillSpector v2.13.4 recursive report.
const report = `{"multi_skill":true,"analysis_completeness":{"status":"complete"},"skills":[
{"name":"helper","path":"000-evil","risk_score":59,"risk_severity":"HIGH","risk_assessment":{"recommendation":"DO_NOT_INSTALL"}},
{"name":"good","path":"001-good","risk_score":0,"risk_severity":"LOW","risk_assessment":{"recommendation":"SAFE"}}]}`

func TestScanParsesAndMapsDirs(t *testing.T) {
	s := New("docker", "img")
	for _, code := range []int{0, 1} { // 1 = findings, not a failure
		s.Exec = func(*exec.Cmd) ([]byte, int, error) { return []byte(report), code, nil }
		rep, err := s.Scan(context.Background(), []string{"/h/evil", "/h/good"}, nil, "")
		if err != nil {
			t.Fatalf("exit %d: %v", code, err)
		}
		if !rep.Complete || len(rep.Skills) != 2 || rep.Skills[0].Dir != "/h/evil" || rep.Skills[1].Dir != "/h/good" {
			t.Fatalf("bad report: %+v", rep)
		}
		if bad := rep.Unsafe(); len(bad) != 1 || bad[0].Name != "helper" {
			t.Errorf("unsafe = %+v", bad)
		}
	}
	s.Exec = func(*exec.Cmd) ([]byte, int, error) { return nil, 2, os.ErrInvalid }
	if _, err := s.Scan(context.Background(), []string{"/h/a"}, nil, ""); err == nil {
		t.Error("exit 2 must fail")
	}
	if _, err := s.Scan(context.Background(), nil, nil, ""); err == nil {
		t.Error("no dirs must fail")
	}
}

func TestSaveLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), LastScanFile)
	if r, err := Load(p); r != nil || err != nil {
		t.Fatalf("missing file: %v %v", r, err)
	}
	in := &Report{Fingerprint: "f", Skills: []Skill{{Name: "a", Recommendation: RecommendBlock}}}
	if err := Save(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(p)
	if err != nil || out.Fingerprint != "f" || len(out.Unsafe()) != 1 {
		t.Fatalf("roundtrip: %+v %v", out, err)
	}
}
