package k8s

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"text/template"

	"github.com/jeefy/convergence/internal/config"
	"github.com/jeefy/convergence/internal/models"
)

//go:embed assets/*
var k8sAssets embed.FS

type ClusterOptions struct {
	Namespace     string
	Provider      string
	CopilotToken  string
	OpenAIKey     string
	AnthropicKey  string
	StandinSalt   string
	OptimizerDSN  string // when set, renders the optimizer CronJob + overlay PVC
	OptSchedule   string
	FailOpen      bool   // Stand-In ext_proc failureMode: failOpen (UNSCRUBBED passthrough) vs failClosed
	OpenAIBaseURL string // testing/self-hosted OpenAI-compatible endpoint for provider=direct
	ModelsFile    string // optional models.yaml override (same schema as ~/.convergence/models.yaml)
	IngressHost   string // when set, renders an Ingress resource pointing to agentgateway:3000
	EnablePDB     bool   // when true, renders PodDisruptionBudgets (minAvailable: 1) for gateway, stand-in, and pinball
	DryRun        bool

	ImageAgentGateway string
	ImageStandIn      string
	ImagePinball      string
	ImageCopilotAPI   string
}

func RenderManifests(opts *ClusterOptions) (string, error) {
	switch opts.Provider {
	case "copilot", "direct":
	default:
		return "", fmt.Errorf("unsupported provider %q: must be copilot or direct", opts.Provider)
	}
	tmplData, err := k8sAssets.ReadFile("assets/manifests.yaml.tmpl")
	if err != nil {
		return "", fmt.Errorf("reading k8s template: %w", err)
	}

	if opts.StandinSalt == "" {
		opts.StandinSalt, _ = config.GenerateSalt()
	}
	if opts.OptSchedule == "" {
		opts.OptSchedule = "0 3 * * 0" // weekly Sunday 03:00
	}

	var override *models.Overrides
	var pinball *models.Pinball
	if opts.ModelsFile != "" {
		raw, err := os.ReadFile(opts.ModelsFile)
		if err != nil {
			return "", fmt.Errorf("reading models file: %w", err)
		}
		o, err := models.ParseOverrides(raw)
		if err != nil {
			return "", err
		}
		override = &o
		if pinball, err = models.ParsePinball(raw); err != nil {
			return "", err
		}
	}
	res, err := models.Resolve(config.ProviderType(opts.Provider), override)
	if err != nil {
		return "", err
	}
	if err := res.ApplyPinball(pinball); err != nil {
		return "", err
	}
	data := newTemplateData(opts, res)

	tmpl, err := template.New("k8s").Parse(string(tmplData))
	if err != nil {
		return "", fmt.Errorf("parsing k8s template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("rendering k8s template: %w", err)
	}
	return buf.String(), nil
}

// DeleteNamespace removes the namespace (and everything in it) installed by RenderManifests.
func DeleteNamespace(ns string) error {
	out, err := exec.Command("kubectl", "delete", "namespace", ns, "--ignore-not-found=true").CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl delete namespace failed: %w (output: %s)", err, string(out))
	}
	return nil
}

func ApplyManifests(manifests string) error {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = bytes.NewBufferString(manifests)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply failed: %w (output: %s)", err, string(out))
	}
	return nil
}

func DeleteManifests(manifests string) error {
	cmd := exec.Command("kubectl", "delete", "-f", "-")
	cmd.Stdin = bytes.NewBufferString(manifests)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl delete failed: %w (output: %s)", err, string(out))
	}
	return nil
}
