package k8s

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderManifestsCopilot(t *testing.T) {
	opts := &ClusterOptions{
		Namespace:    "convergence-test",
		Provider:     "copilot",
		CopilotToken: "test_token_copilot",
		StandinSalt:  "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}

	manifests, err := RenderManifests(opts)
	if err != nil {
		t.Fatalf("RenderManifests failed: %v", err)
	}

	if !strings.Contains(manifests, "name: convergence-test") {
		t.Errorf("manifests missing namespace")
	}
	if !strings.Contains(manifests, "name: convergence-copilot") {
		t.Errorf("manifests missing copilot secret")
	}
	if !strings.Contains(manifests, "stand-in.convergence-test.svc.cluster.local:9101") {
		t.Errorf("manifests missing stand-in extProc")
	}
	if !strings.Contains(manifests, "image: cr.agentgateway.dev/agentgateway:v1.6.0") {
		t.Errorf("manifests missing agentgateway image")
	}
}

func TestRenderManifestsDirect(t *testing.T) {
	opts := &ClusterOptions{
		Namespace:    "convergence-direct",
		Provider:     "direct",
		OpenAIKey:    "sk-test-direct",
		AnthropicKey: "sk-ant-direct",
	}

	manifests, err := RenderManifests(opts)
	if err != nil {
		t.Fatalf("RenderManifests failed: %v", err)
	}

	if strings.Contains(manifests, "name: convergence-copilot") {
		t.Errorf("direct provider should not have copilot secret")
	}
	if !strings.Contains(manifests, "sk-ant-direct") {
		t.Errorf("manifests missing Anthropic key (default direct mapping is anthropic)")
	}
}

func renderDocs(t *testing.T, opts *ClusterOptions) []map[string]any {
	t.Helper()
	out, err := RenderManifests(opts)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	dec := yaml.NewDecoder(strings.NewReader(out))
	var docs []map[string]any
	for {
		var d map[string]any
		if err := dec.Decode(&d); err != nil {
			if errors.Is(err, io.EOF) {
				return docs
			}
			t.Fatalf("invalid YAML: %v", err)
		}
		if d != nil {
			docs = append(docs, d)
		}
	}
}

func findKind(docs []map[string]any, kind string) map[string]any {
	for _, d := range docs {
		if d["kind"] == kind {
			return d
		}
	}
	return nil
}

func TestRenderValidYAMLMatrix(t *testing.T) {
	for _, prov := range []string{"copilot", "direct"} {
		for _, dsn := range []string{"", "postgres://u:p@h/db"} {
			docs := renderDocs(t, &ClusterOptions{Namespace: "x", Provider: prov, OptimizerDSN: dsn})
			if len(docs) == 0 {
				t.Fatalf("%s/%q: no docs", prov, dsn)
			}
			if (findKind(docs, "CronJob") != nil) != (dsn != "") {
				t.Errorf("%s/%q: CronJob presence wrong", prov, dsn)
			}
			if (findKind(docs, "PersistentVolumeClaim") != nil) != (dsn != "") {
				t.Errorf("%s/%q: PVC presence wrong", prov, dsn)
			}
		}
	}
}

func TestOptSchedule(t *testing.T) {
	out, err := RenderManifests(&ClusterOptions{Namespace: "x", Provider: "direct", OptimizerDSN: "d", OptSchedule: "5 4 * * 1"})
	if err != nil || !strings.Contains(out, `schedule: "5 4 * * 1"`) {
		t.Fatalf("schedule override not honored: %v", err)
	}
	out, _ = RenderManifests(&ClusterOptions{Namespace: "x", Provider: "direct", OptimizerDSN: "d"})
	if !strings.Contains(out, `schedule: "0 3 * * 0"`) {
		t.Errorf("default schedule missing")
	}
}

func TestInvalidProviderRejected(t *testing.T) {
	if _, err := RenderManifests(&ClusterOptions{Namespace: "x", Provider: "ollama"}); err == nil {
		t.Fatal("expected error for ollama")
	}
}

func TestNoCommandOverridesEntrypoint(t *testing.T) {
	out, _ := RenderManifests(&ClusterOptions{Namespace: "x", Provider: "copilot"})
	if strings.Contains(out, `command: ["-f"`) {
		t.Error("agentgateway must use args")
	}
}

func cfgData(t *testing.T, opts *ClusterOptions) string {
	t.Helper()
	for _, d := range renderDocs(t, opts) {
		if d["kind"] == "Secret" {
			if sd, ok := d["stringData"].(map[string]any); ok {
				if c, ok := sd["config.yaml"].(string); ok {
					return c
				}
			}
		}
	}
	t.Fatal("agentgateway config not found")
	return ""
}

func TestStandInFailureModeDefaultsClosed(t *testing.T) {
	closed := cfgData(t, &ClusterOptions{Namespace: "x", Provider: "direct"})
	if !strings.Contains(closed, "failureMode: failClosed") || strings.Contains(closed, "failureMode: failOpen") {
		t.Errorf("default must be failClosed:\n%s", closed)
	}
	open := cfgData(t, &ClusterOptions{Namespace: "x", Provider: "direct", FailOpen: true})
	if !strings.Contains(open, "failureMode: failOpen") || strings.Contains(open, "failureMode: failClosed") {
		t.Errorf("FailOpen must flip to failOpen")
	}
	if !strings.Contains(closed, "failureMode: allow") {
		t.Errorf("pinball ext_authz must stay allow")
	}
}

func TestMatrixNoSecretsInConfigMaps(t *testing.T) {
	for _, prov := range []string{"copilot", "direct"} {
		for _, dsn := range []string{"", "postgres://u:p@h/db"} {
			for _, fo := range []bool{false, true} {
				docs := renderDocs(t, &ClusterOptions{
					Namespace: "x", Provider: prov, OptimizerDSN: dsn, FailOpen: fo,
					CopilotToken: "SECRETCOPILOT", OpenAIKey: "SECRETOPENAI", AnthropicKey: "SECRETANTHROPIC",
				})
				for _, d := range docs {
					if d["kind"] != "ConfigMap" {
						continue
					}
					raw, _ := yaml.Marshal(d)
					for _, s := range []string{"SECRETCOPILOT", "SECRETOPENAI", "SECRETANTHROPIC", "postgres://"} {
						if strings.Contains(string(raw), s) {
							t.Errorf("%s/%q/%v: ConfigMap contains %s", prov, dsn, fo, s)
						}
					}
				}
			}
		}
	}
}

func TestModelsFileOverrideAndErrors(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "m.yaml")
	if err := os.WriteFile(good, []byte("direct:\n  claude-haiku-5.5: \"openai:my-mini\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := RenderManifests(&ClusterOptions{Namespace: "x", Provider: "direct", ModelsFile: good, OpenAIKey: "k", AnthropicKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "model: my-mini") || !strings.Contains(out, "name: openai-direct") || !strings.Contains(out, "name: anthropic-direct") {
		t.Errorf("override not applied:\n%s", out)
	}
	if _, err := RenderManifests(&ClusterOptions{Namespace: "x", Provider: "direct", ModelsFile: filepath.Join(dir, "nope.yaml")}); err == nil {
		t.Error("missing models file must error")
	}
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("direct:\n  not-a-model: foo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RenderManifests(&ClusterOptions{Namespace: "x", Provider: "direct", ModelsFile: bad}); err == nil {
		t.Error("unknown pinball model must error")
	}
}

func TestNoHardcodedModelNamesAndMappingSync(t *testing.T) {
	tmpl, err := k8sAssets.ReadFile("assets/manifests.yaml.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude-", "gemini-", "gpt-"} {
		if strings.Contains(string(tmpl), name) {
			t.Errorf("template hardcodes model name %q", name)
		}
	}
	out, _ := RenderManifests(&ClusterOptions{Namespace: "x", Provider: "copilot"})
	for _, n := range []string{"claude-haiku-5.5", "claude-sonnet-5.5", "claude-opus-5.5"} {
		if strings.Count(out, n) < 3 {
			t.Errorf("%s should appear in pinball.yaml and gateway config", n)
		}
	}
}

func TestOpenAIBaseURL(t *testing.T) {
	c := cfgData(t, &ClusterOptions{Namespace: "x", Provider: "direct", ModelsFile: openaiModels(t), OpenAIBaseURL: "http://s:1/v1"})
	if !strings.Contains(c, `baseUrl: "http://s:1/v1"`) {
		t.Error("base url not rendered")
	}
	if strings.Contains(cfgData(t, &ClusterOptions{Namespace: "x", Provider: "direct", ModelsFile: openaiModels(t)}), "baseUrl") {
		t.Error("baseUrl must be absent by default")
	}
}

func TestKubectlWrappersReportErrors(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := ApplyManifests("a: b"); err == nil {
		t.Error("apply without kubectl must error")
	}
	if err := DeleteManifests("a: b"); err == nil {
		t.Error("delete without kubectl must error")
	}
	if err := DeleteNamespace("x"); err == nil {
		t.Error("delete namespace without kubectl must error")
	}
}

func openaiModels(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "m.yaml")
	y := "direct:\n  claude-haiku-5.5: \"openai:a\"\n  claude-sonnet-5.5: \"openai:a\"\n  claude-opus-5.5: \"openai:a\"\n"
	if err := os.WriteFile(p, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestAgentgatewayUsesLongFileFlag(t *testing.T) {
	out, err := RenderManifests(&ClusterOptions{Namespace: "x", Provider: "copilot"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `args: ["--file", "/etc/agentgateway/config.yaml"]`) {
		t.Error("agentgateway must be started with --file; with -f it exits: configuration is required when running in Kubernetes")
	}
}

func TestClusterCustomImageOverrides(t *testing.T) {
	customAGW := "ghcr.io/agentgateway/agentgateway:sha-mario4140"
	customStandIn := "ghcr.io/jeefy/stand-in:v0.4.2-test"
	customPinball := "ghcr.io/jeefy/pinball:dev-custom"
	customCopilot := "ghcr.io/ericc-ch/copilot-api:edge-custom"

	opts := &ClusterOptions{
		Namespace:         "custom-ns",
		Provider:          "copilot",
		ImageAgentGateway: customAGW,
		ImageStandIn:      customStandIn,
		ImagePinball:      customPinball,
		ImageCopilotAPI:   customCopilot,
	}

	manifests, err := RenderManifests(opts)
	if err != nil {
		t.Fatalf("RenderManifests failed: %v", err)
	}

	for _, expected := range []string{customAGW, customStandIn, customPinball, customCopilot} {
		if !strings.Contains(manifests, expected) {
			t.Errorf("manifests do not contain overridden image %s", expected)
		}
	}
}

func TestClusterIngressAndPDB(t *testing.T) {
	opts := &ClusterOptions{
		Namespace:   "ingress-test",
		Provider:    "direct",
		OpenAIKey:   "sk-test",
		IngressHost: "ai.example.com",
		EnablePDB:   true,
	}
	manifests, err := RenderManifests(opts)
	if err != nil {
		t.Fatalf("RenderManifests failed: %v", err)
	}
	if !strings.Contains(manifests, "kind: Ingress") || !strings.Contains(manifests, "host: ai.example.com") {
		t.Errorf("missing Ingress resource for ai.example.com")
	}
	if !strings.Contains(manifests, "kind: PodDisruptionBudget") || !strings.Contains(manifests, "name: agentgateway-pdb") {
		t.Errorf("missing PodDisruptionBudget resources")
	}
}

func TestCopilotAPIIdleTimeoutShim(t *testing.T) {
	manifests, err := RenderManifests(&ClusterOptions{
		Namespace:    "convergence-test",
		Provider:     "copilot",
		CopilotToken: "test_token_copilot",
		StandinSalt:  "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	})
	if err != nil {
		t.Fatalf("RenderManifests failed: %v", err)
	}
	for _, want := range []string{
		"name: copilot-api-idle-timeout",
		"idleTimeout: options.idleTimeout ?? 0",
		`preload = ["/etc/copilot-api/idle-timeout.js"]`,
		"mountPath: /app/bunfig.toml",
	} {
		if !strings.Contains(manifests, want) {
			t.Errorf("manifests missing %q", want)
		}
	}
}

func TestModelsFilePinballOverride(t *testing.T) {
	f := filepath.Join(t.TempDir(), "m.yaml")
	y := "pinball:\n  thresholds: {standard: 2, heavy: 5}\n  scoring: {tools: 0}\n"
	if err := os.WriteFile(f, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := RenderManifests(&ClusterOptions{Namespace: "x", Provider: "copilot", ModelsFile: f})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"standard: 2", "heavy: 5", "scoring:\n      tools: 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	out, _ = RenderManifests(&ClusterOptions{Namespace: "x", Provider: "copilot"})
	if strings.Contains(out, "scoring:") || !strings.Contains(out, "standard: 1") {
		t.Error("defaults changed")
	}
}
