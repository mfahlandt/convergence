package clientconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNamesAndEnvExports(t *testing.T) {
	n := Names()
	if len(n) == 0 || n[len(n)-1] != "oh-my-opencode" {
		t.Errorf("names %v", n)
	}
	e := EnvExports(gw)
	for _, w := range []string{`OPENAI_BASE_URL="http://localhost:3000/v1"`, `ANTHROPIC_BASE_URL="http://localhost:3000"`} {
		if !strings.Contains(e, w) {
			t.Errorf("missing %s in %s", w, e)
		}
	}
}

func TestUnknownClient(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := Configure("nope", Options{GatewayURL: gw}); err == nil || !strings.Contains(err.Error(), "unknown client") {
		t.Errorf("configure: %v", err)
	}
	if _, err := Restore("nope", Options{}); err == nil {
		t.Error("restore should fail")
	}
}

func TestRestoreNothingAndCorruptManifest(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	if _, err := Restore("claude-code", Options{}); err == nil || !strings.Contains(err.Error(), "nothing to restore") {
		t.Errorf("got %v", err)
	}
	p := filepath.Join(h, ".claude/settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p+manifestSuffix, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore("claude-code", Options{}); err == nil || !strings.Contains(err.Error(), "corrupt manifest") {
		t.Errorf("got %v", err)
	}
}

func TestEmptyFileAndTrailingCommasAllJSONClients(t *testing.T) {
	for _, f := range fixtures {
		if !strings.HasSuffix(f.rel, ".json") {
			continue
		}
		for name, content := range map[string]string{"empty": "", "ws": "  \n", "emptyobj": "{}", "tc": "{\"a\": [1,2,],\n}\n"} {
			t.Run(f.client+"/"+name, func(t *testing.T) {
				h := t.TempDir()
				t.Setenv("HOME", h)
				p := filepath.Join(h, f.rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := Configure(f.client, Options{GatewayURL: gw}); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(p + backupSuffix); err != nil {
					t.Errorf("backup missing: %v", err)
				}
				if _, err := Restore(f.client, Options{}); err != nil {
					t.Fatal(err)
				}
				if got := read(t, p); got != content {
					t.Errorf("restore not exact: %q vs %q", got, content)
				}
			})
		}
	}
}

func TestForceRestoreRemovesFreshFileWhenOnlyOursRemain(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.client, func(t *testing.T) {
			p := setup(t, f, false)
			if _, err := Configure(f.client, Options{GatewayURL: gw}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, append([]byte(read(t, p)), '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Restore(f.client, Options{}); err == nil {
				t.Fatal("expected refusal")
			}
			if _, err := Restore(f.client, Options{Force: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(p); err == nil {
				// remaining empty parent containers are tolerated; our values are not
				if got := strings.ToLower(read(t, p)); strings.Contains(got, "localhost:3000") || strings.Contains(got, "convergence") {
					t.Errorf("our keys survived: %s", got)
				}
			}
			if _, err := os.Stat(p + backupSuffix); !os.IsNotExist(err) {
				t.Error("backup should be cleaned")
			}
		})
	}
}

func TestForceRestoreKeepsCommentsAndUserKeys(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.client, func(t *testing.T) {
			p := setup(t, f, true)
			if _, err := Configure(f.client, Options{GatewayURL: gw}); err != nil {
				t.Fatal(err)
			}
			if _, err := Restore(f.client, Options{Force: true}); err != nil {
				t.Fatal(err)
			}
			got := read(t, p)
			for _, k := range f.keep {
				if !strings.Contains(got, k) {
					t.Errorf("lost %q in %s", k, got)
				}
			}
		})
	}
}

func TestRestoreWhenFileDeletedAndDryRun(t *testing.T) {
	f := fixtures[1]
	p := setup(t, f, true)
	if _, err := Configure(f.client, Options{GatewayURL: gw}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(f.client, Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + manifestSuffix); err != nil {
		t.Error("dry run must keep manifest")
	}
	if _, err := Restore(f.client, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + manifestSuffix); !os.IsNotExist(err) {
		t.Error("manifest should be cleaned")
	}
}

func TestForceRestoreDryRunWritesNothing(t *testing.T) {
	f := fixtures[0]
	p := setup(t, f, true)
	if _, err := Configure(f.client, Options{GatewayURL: gw}); err != nil {
		t.Fatal(err)
	}
	edited := read(t, p) + "// user\n"
	if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Restore(f.client, Options{Force: true, DryRun: true})
	if err != nil || r.Diff == "" {
		t.Fatalf("%v %q", err, r.Diff)
	}
	if read(t, p) != edited {
		t.Error("dry run modified file")
	}
}

func TestInvalidJSONRejectedAndUntouched(t *testing.T) {
	f := fixtures[0]
	p := setup(t, f, false)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := "{ \"a\": /* unterminated "
	if err := os.WriteFile(p, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Configure(f.client, Options{GatewayURL: gw}); err == nil {
		t.Error("expected error")
	}
	if read(t, p) != bad {
		t.Error("file touched")
	}
}

func TestRevertEditsErrors(t *testing.T) {
	c, _, err := find("claude-code")
	if err != nil {
		t.Skip()
	}
	if _, err := revertEdits(c, []byte("{bad"), nil); err == nil {
		t.Error("expected parse error")
	}
	src := []byte(`{"a": 1}`)
	bad := []edit{{Path: []string{"a"}, Had: true, Prev: []byte("{x"), New: []byte("1")}}
	if _, err := revertEdits(c, src, bad); err == nil {
		t.Error("expected prev unmarshal error")
	}
	out, err := revertEdits(c, src, []edit{{Path: []string{"a"}, Had: true, Prev: []byte("0"), New: []byte("1")}})
	if err != nil || !strings.Contains(string(out), "0") {
		t.Errorf("%s %v", out, err)
	}
}

func TestIsEmptyDoc(t *testing.T) {
	c, _, _ := find("claude-code")
	if !isEmptyDoc(c, []byte("{}")) || isEmptyDoc(c, []byte(`{"a":1}`)) || isEmptyDoc(c, []byte("{bad")) {
		t.Error("isEmptyDoc wrong")
	}
}

func TestWriteAtomicErrors(t *testing.T) {
	d := t.TempDir()
	file := filepath.Join(d, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(filepath.Join(file, "sub", "x"), []byte("a"), 0o644); err == nil {
		t.Error("mkdir under a file should fail")
	}
	// target is a directory: rename fails
	dir := filepath.Join(d, "dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "k"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(dir, []byte("a"), 0o644); err == nil {
		t.Error("rename onto non-empty dir should fail")
	}
	// existing mode preserved
	m := filepath.Join(d, "m")
	if err := os.WriteFile(m, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(m, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(m); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
	if ents, _ := os.ReadDir(d); len(ents) != 3 {
		t.Errorf("temp file leaked: %v", ents)
	}
}

func TestReadManifestUnreadableAndCommitErrors(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "c.json")
	if err := os.Mkdir(p+manifestSuffix, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(p); err == nil {
		t.Error("expected read error")
	}
	if err := commit(p, nil, false, []byte("{}"), nil); err == nil {
		t.Error("commit should surface manifest error")
	}
	if _, _, err := readOptional(d); err == nil {
		t.Error("reading a dir should fail")
	}
}

func TestMergeEditsKeepsFirstPrev(t *testing.T) {
	old := []edit{{Path: []string{"a"}, Had: true, Prev: []byte("1"), New: []byte("2")}}
	got := mergeEdits(old, []edit{{Path: []string{"a"}, Had: true, Prev: []byte("2"), New: []byte("3")}, {Path: []string{"b"}, New: []byte("1")}})
	if len(got) != 2 || string(got[0].Prev) != "1" || string(got[0].New) != "3" {
		t.Errorf("%+v", got)
	}
}

func TestSecondConfigureAfterUserEditMarksDirty(t *testing.T) {
	f := fixtures[1]
	p := setup(t, f, true)
	if _, err := Configure(f.client, Options{GatewayURL: gw}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(read(t, p), "{", "{\n  \"mine\": true,", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Configure(f.client, Options{GatewayURL: "http://other:1"}); err != nil {
		t.Fatal(err)
	}
	m, _ := readManifest(p)
	if m == nil || !m.Dirty {
		t.Fatalf("expected dirty manifest: %+v", m)
	}
	if _, err := Restore(f.client, Options{}); err == nil {
		t.Error("dirty restore must require force")
	}
	if _, err := Restore(f.client, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); !strings.Contains(got, `"mine": true`) || strings.Contains(got, "ANTHROPIC_BASE_URL") {
		t.Errorf("force restore result %s", got)
	}
}

func TestStripJSONC(t *testing.T) {
	in := "{ /* c\n x */ \"a\": \"//no\\\"/*\", // t\n \"b\": [1,\n],\n \"c\": 1, /* unterminated"
	out := string(stripJSONC([]byte(in)))
	if len(out) != len(in) {
		t.Fatal("offsets must be preserved")
	}
	for _, w := range []string{`"//no\"/*"`} {
		if !strings.Contains(out, w) {
			t.Errorf("string mangled: %s", out)
		}
	}
	if strings.Contains(out, "// t") || strings.Contains(out, "x */") || strings.Contains(out, "1,\n]") {
		t.Errorf("not stripped: %q", out)
	}
	if got := string(stripJSONC([]byte("/* a */"))); strings.TrimSpace(got) != "" {
		t.Errorf("%q", got)
	}
	_ = stripJSONC([]byte("/"))
	_ = stripJSONC([]byte(`"abc\`))
}

func TestScannerErrors(t *testing.T) {
	for _, in := range []string{"", "[1]", "{", "{ /* x", "{1:2}", `{"a" 1}`, `{"a": 1`, `{"a"`, `{"a`, `{"a": /* x`, `{"a": "b`, `{"a": {`} {
		s := &scanner{b: []byte(in)}
		if _, err := s.object(); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
	s := &scanner{b: []byte("  // c\n /* d */ {\"a\":1,,\"b\":{}}")}
	o, err := s.object()
	if err != nil || len(o.members) != 2 || o.find("zzz") != nil || o.find("b") == nil {
		t.Errorf("%+v %v", o, err)
	}
}

func TestJSONSetDeleteEdges(t *testing.T) {
	// set into empty / whitespace-only objects and nested creation
	for _, src := range []string{"{}", "{\n}", "{ /* c */ }", "{\n  \"x\": 1\n}", "{\"x\": 1, // t\n}"} {
		out, err := jsonSet([]byte(src), []string{"a", "b", "c"}, "v")
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		d, err := jsonGet(out)
		if err != nil {
			t.Fatalf("%q -> %q: %v", src, out, err)
		}
		if v, ok := lookup(d, "a", "b", "c"); !ok || v != "v" {
			t.Errorf("%q -> %q", src, out)
		}
		back, err := jsonDelete(out, []string{"a", "b", "c"})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := lookupDoc(t, back, "a", "b", "c"); ok {
			t.Errorf("not deleted: %s", back)
		}
	}
	// overwrite a scalar parent with an object value, replace existing
	out, err := jsonSet([]byte(`{"a": 1}`), []string{"a"}, map[string]any{"k": []any{1, "x"}})
	if err != nil || !strings.Contains(string(out), `"k"`) {
		t.Errorf("%s %v", out, err)
	}
	if _, err := jsonSet([]byte(`{"a": 1}`), []string{"a", "b"}, 1); err == nil {
		t.Log("setting beneath scalar returned no error")
	}
	if _, err := jsonSet([]byte("{bad"), []string{"a"}, 1); err == nil {
		t.Error("expected error")
	}
	if _, err := jsonSet([]byte("{}"), []string{"a"}, make(chan int)); err == nil {
		t.Error("unmarshalable value")
	}
	// delete: first/middle/last/only/absent/non-object parent/invalid
	src := `{"a": 1, "b": {"c": 2}, "d": 3}`
	for _, p := range [][]string{{"a"}, {"b"}, {"d"}, {"b", "c"}, {"zz"}, {"a", "x"}, {"zz", "y"}} {
		out, err := jsonDelete([]byte(src), p)
		if err != nil {
			t.Fatalf("%v: %v", p, err)
		}
		if _, ok := lookupDoc(t, out, p...); ok {
			t.Errorf("%v not deleted: %s", p, out)
		}
	}
	if _, err := jsonDelete([]byte("{bad"), pathA); err == nil {
		t.Error("expected error")
	}
	if _, err := jsonDelete([]byte(`{"a": {bad}}`), []string{"a", "b"}); err == nil {
		t.Error("expected nested error")
	}
}

func lookupDoc(t *testing.T, b []byte, p ...string) (any, bool) {
	t.Helper()
	d, err := jsonGet(b)
	if err != nil {
		t.Fatalf("invalid json %q: %v", b, err)
	}
	return lookup(d, p...)
}

func TestJSONGetEmptyAndInvalid(t *testing.T) {
	if d, err := jsonGet(nil); err != nil || len(d) != 0 {
		t.Errorf("%v %v", d, err)
	}
	if _, err := jsonGet([]byte("[1]")); err == nil {
		t.Error("array root should fail")
	}
	if _, err := jsonGet([]byte("{")); err == nil {
		t.Error("expected error")
	}
}

func TestYAMLEdges(t *testing.T) {
	src := []byte("# top\na:\n  b: 1\n  c: 2\nlist:\n  - name: x\n    v: 1\n  - name: y\n")
	for _, p := range [][]string{{"a", "b"}, {"a"}, {"zz"}, {"a", "zz", "q"}, {"list", itemPrefix + "x", "v"}, {"list", itemPrefix + "x"}, {"list", itemPrefix + "nope"}, {"list", itemPrefix + "nope", "v"}} {
		out, err := yamlDelete(src, p)
		if err != nil {
			t.Fatalf("%v: %v", p, err)
		}
		if len(p) > 1 && p[0] != "a" && !strings.Contains(string(out), "# top") {
			t.Errorf("%v lost comment: %s", p, out)
		}
	}
	if _, err := yamlDelete([]byte("a: [unclosed"), pathA); err == nil {
		t.Error("expected parse error")
	}
	if _, err := yamlGet([]byte("a: [unclosed")); err == nil {
		t.Error("expected parse error")
	}
	if _, err := yamlSet([]byte("a: [unclosed"), pathA, 1); err == nil {
		t.Error("expected parse error")
	}
	if d, err := yamlGet(nil); err != nil || len(d) != 0 {
		t.Errorf("%v %v", d, err)
	}
	if _, err := yamlGet([]byte("- a\n- b\n")); err == nil {
		t.Log("sequence root accepted")
	}
	// set into empty, scalar-replaced-by-map, and sequence items
	out, err := yamlSet(nil, []string{"x", "y"}, "v")
	if err != nil || !strings.Contains(string(out), "y: v") {
		t.Errorf("%s %v", out, err)
	}
	out, err = yamlSet([]byte("x: 1\n"), []string{"x", "y"}, "v")
	if err == nil {
		t.Errorf("scalar parent must be refused, got %s", out)
	}
	out, err = yamlSet(src, []string{"list", itemPrefix + "x", "v"}, 9)
	if err != nil || !strings.Contains(string(out), "v: 9") {
		t.Errorf("%s %v", out, err)
	}
	out, err = yamlSet(src, []string{"list", itemPrefix + "new"}, map[string]any{"name": "new"})
	if err != nil || !strings.Contains(string(out), "new") {
		t.Errorf("%s %v", out, err)
	}
	out, err = yamlSet([]byte("m: 1\n"), []string{"fresh", itemPrefix + "q"}, map[string]any{"name": "q"})
	if err != nil || !strings.Contains(string(out), "fresh") {
		t.Errorf("%s %v", out, err)
	}
}

var pathA = []string{"a", "b"}

func TestHermesHomeOverride(t *testing.T) {
	d := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("HERMES_HOME", d)
	r, err := Configure("hermes", Options{GatewayURL: gw})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(d, "config.yaml"); r.Path != want {
		t.Errorf("path = %s, want %s", r.Path, want)
	}
	if !exists(filepath.Join(d, "config.yaml")) {
		t.Error("config.yaml not written under HERMES_HOME")
	}
}

func TestHermesHomeWindowsDefault(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("LOCALAPPDATA default is Windows-only")
	}
	d := t.TempDir()
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HERMES_HOME", "")
	t.Setenv("LOCALAPPDATA", d)
	if got, want := HermesHome("unused"), filepath.Join(d, "hermes"); got != want {
		t.Errorf("HermesHome = %s, want %s", got, want)
	}
}
