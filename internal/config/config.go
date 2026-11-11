package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type ProviderType string

const (
	ProviderCopilot ProviderType = "copilot"
	ProviderDirect  ProviderType = "direct"
	ProviderOllama  ProviderType = "ollama"
)

type SandboxType string

const (
	SandboxNone      SandboxType = "none"
	SandboxOpenShell SandboxType = "openshell"
	SandboxNono      SandboxType = "nono"
	SandboxDockerSbx SandboxType = "docker-sbx"
	SandboxK8sAgent  SandboxType = "k8s-agent"
)

func ParseSandbox(s string) (SandboxType, error) {
	switch sbx := SandboxType(s); sbx {
	case "", SandboxNone:
		return SandboxNone, nil
	case SandboxOpenShell, SandboxNono, SandboxDockerSbx, SandboxK8sAgent:
		return sbx, nil
	default:
		return "", fmt.Errorf("unknown sandbox %q (want none, openshell, nono, docker-sbx or k8s-agent)", s)
	}
}

// ParseProvider converts user input into a known ProviderType.
func ParseProvider(s string) (ProviderType, error) {
	switch p := ProviderType(s); p {
	case ProviderCopilot, ProviderDirect, ProviderOllama:
		return p, nil
	default:
		return "", fmt.Errorf("unknown provider %q (want copilot, direct or ollama)", s)
	}
}

const (
	DefaultGatewayPort = 3000
	DefaultBindAddress = "127.0.0.1"
	DefaultOptSchedule = "0 3 * * 0" // weekly Sunday 03:00
	DefaultOllamaURL   = "http://host.docker.internal:11434"
)

// Fixed host ports published by the local compose stack (besides the gateway port).
const (
	StandinMetricsPort  = 9102
	PinballPort         = 9201
	GatewayStatsPort    = 15020
	CopilotAPIPort      = 4141
	OpenShellPort       = 17670
	OpenShellHealthPort = 17671
)

type Config struct {
	Engine       string       `yaml:"engine"`                 // "docker" or "podman"
	Provider     ProviderType `yaml:"provider"`               // "copilot", "direct", "ollama"
	Sandbox      SandboxType  `yaml:"sandbox,omitempty"`      // "none", "openshell", "docker-sbx", "k8s-agent"
	GatewayPort  int          `yaml:"gateway_port"`           // default 3000
	BindAddress  string       `yaml:"bind_address"`           // host address published ports bind to; default 127.0.0.1
	StandinSalt  string       `yaml:"standin_salt"`           // 32-byte hex string
	OptSchedule  string       `yaml:"opt_schedule,omitempty"` // cron expression
	CopilotToken string       `yaml:"copilot_token,omitempty"`
	OpenAIKey    string       `yaml:"openai_key,omitempty"`
	AnthropicKey string       `yaml:"anthropic_key,omitempty"`
	OllamaURL    string       `yaml:"ollama_url,omitempty"`
	// FailOpen lets requests continue UNSCRUBBED when Stand-In is down.
	// Default false: the gateway rejects them (fail-closed).
	FailOpen bool `yaml:"fail_open,omitempty"`
	// RequestLogDB is a Postgres DSN for agentgateway's request log, needed by
	// `convergence optimize`. Empty = off. Logging is "full", so the database
	// holds prompts and completions: treat the DSN and the database as secret.
	RequestLogDB string `yaml:"request_log_db,omitempty"`
	// OTel enables OTLP trace export (off unless otel.endpoint is set).
	OTel OTel `yaml:"otel,omitempty"`
	// StandinPatternsFile is an absolute host path to a Stand-In patterns.yaml; compose mounts it
	// read-only and sets STANDIN_PATTERNS_FILE. Empty = the image default.
	StandinPatternsFile string `yaml:"standin_patterns_file,omitempty"`
	// Ollama model overrides per tier ("" = internal/models default).
	// ~/.convergence/models.yaml wins over these.
	OllamaModelLight    string `yaml:"ollama_model_light,omitempty"`
	OllamaModelStandard string `yaml:"ollama_model_standard,omitempty"`
	OllamaModelHeavy    string `yaml:"ollama_model_heavy,omitempty"`

	ComposeProvider string `yaml:"compose_provider,omitempty"`

	ImageAgentGateway     string `yaml:"image_agentgateway,omitempty"`
	ImageStandIn          string `yaml:"image_standin,omitempty"`
	ImagePinball          string `yaml:"image_pinball,omitempty"`
	ImageCopilotAPI       string `yaml:"image_copilot_api,omitempty"`
	ImageOpenShellGateway string `yaml:"image_openshell_gateway,omitempty"`
	ImageSkillSpector     string `yaml:"image_skillspector,omitempty"`

	// SkillScan gates `sandbox run` on a SkillSpector scan of installed skills:
	// "off" (default, opt-in), "warn" or "block". SkillScanModel is the gateway model
	// for `skills scan --llm`; it must honor forced tool calls.
	SkillScan      SkillScanMode `yaml:"skill_scan,omitempty"`
	SkillScanModel string        `yaml:"skill_scan_model,omitempty"`

	// ExtProcTimeout is agentgateway's requestTimeout for the ext_proc
	// (stand-in) policy: how long it waits for the first gRPC response headers.
	// Go duration, > 0. Empty = DefaultExtProcTimeout.
	ExtProcTimeout string `yaml:"extproc_timeout,omitempty"`
}

// SkillScanMode is how `sandbox run` reacts to unsafe installed skills.
type SkillScanMode string

const (
	SkillScanWarn  SkillScanMode = "warn"
	SkillScanBlock SkillScanMode = "block"
	SkillScanOff   SkillScanMode = "off"
)

// SkillScanOrDefault returns the configured mode, or off when unset: the scan
// needs a locally built image, so users opt in.
func (c *Config) SkillScanOrDefault() SkillScanMode {
	if c.SkillScan == "" {
		return SkillScanOff
	}
	return c.SkillScan
}

// ValidateSkillScan rejects an unknown skill_scan mode.
func (c *Config) ValidateSkillScan() error {
	switch c.SkillScanOrDefault() {
	case SkillScanWarn, SkillScanBlock, SkillScanOff:
		return nil
	}
	return fmt.Errorf("invalid skill_scan %q (want warn, block or off)", c.SkillScan)
}

// DefaultExtProcTimeout is the ext_proc requestTimeout used when unset.
const DefaultExtProcTimeout = "5s"

// ExtProcTimeoutOrDefault returns s, or DefaultExtProcTimeout when empty.
func ExtProcTimeoutOrDefault(s string) string {
	if s == "" {
		return DefaultExtProcTimeout
	}
	return s
}

// ValidateExtProcTimeout checks that s (empty allowed) is a positive Go duration.
func ValidateExtProcTimeout(s string) error {
	if s == "" {
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid extproc timeout %q: %w (use a Go duration such as 5s or 1m)", s, err)
	}
	if d <= 0 {
		return fmt.Errorf("invalid extproc timeout %q: must be greater than 0", s)
	}
	return nil
}

// ExtProcTimeoutValue is the effective timeout for templates.
func (c *Config) ExtProcTimeoutValue() string { return ExtProcTimeoutOrDefault(c.ExtProcTimeout) }

func (c *Config) ValidateComposeProvider() error {
	switch c.ComposeProvider {
	case "", "auto":
		return nil
	case "docker-compose", "podman-compose":
		if c.Engine != "podman" {
			return fmt.Errorf("compose_provider %q requires engine podman", c.ComposeProvider)
		}
		return nil
	default:
		return fmt.Errorf("invalid compose_provider %q (want auto, docker-compose or podman-compose)", c.ComposeProvider)
	}
}

// OTel configures optional OTLP trace export from agentgateway. Zero value
// (no endpoint) = off and the rendered config is unchanged.
type OTel struct {
	Endpoint string  `yaml:"endpoint,omitempty"` // collector host:port
	Protocol string  `yaml:"protocol,omitempty"` // "grpc" (default) or "http"
	Sample   float64 `yaml:"sample,omitempty"`   // 0..1; 0 = default 1
}

func (o OTel) Enabled() bool { return o.Endpoint != "" }

func (o OTel) ProtocolOrDefault() string {
	if o.Protocol == "" {
		return "grpc"
	}
	return o.Protocol
}

func (o OTel) SampleOrDefault() float64 {
	if o.Sample == 0 {
		return 1
	}
	return o.Sample
}

// Validate rejects a protocol or sample rate agentgateway would not accept.
func (o OTel) Validate() error {
	if p := o.ProtocolOrDefault(); p != "grpc" && p != "http" {
		return fmt.Errorf("invalid otel protocol %q (want grpc or http)", p)
	}
	if o.Sample < 0 || o.Sample > 1 {
		return fmt.Errorf("invalid otel sample %v (want 0..1)", o.Sample)
	}
	return nil
}

// NamedPort is a host port the stack publishes, with the service that owns it.
type NamedPort struct {
	Name string
	Port int
}

// PublishedPorts lists every host port the rendered compose file publishes.
func (c *Config) PublishedPorts() []NamedPort {
	ports := []NamedPort{
		{"agentgateway", c.GatewayPort},
		{"agentgateway stats", GatewayStatsPort},
		{"stand-in metrics", StandinMetricsPort},
		{"pinball", PinballPort},
	}
	if c.Provider == ProviderCopilot {
		ports = append(ports, NamedPort{"copilot-api", CopilotAPIPort})
	}
	if c.Sandbox == SandboxOpenShell {
		ports = append(ports, NamedPort{"openshell", OpenShellPort})
		ports = append(ports, NamedPort{"openshell health", OpenShellHealthPort})
	}
	return ports
}

// HealthHost is the host address to reach published ports from this machine.
func (c *Config) HealthHost() string {
	switch c.BindAddress {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return c.BindAddress
	}
}

func DefaultConfig() *Config {
	salt, _ := GenerateSalt()
	return &Config{
		Engine:      "docker",
		Provider:    ProviderDirect,
		Sandbox:     SandboxNone,
		GatewayPort: DefaultGatewayPort,
		BindAddress: DefaultBindAddress,
		StandinSalt: salt,
		OptSchedule: DefaultOptSchedule,
		OllamaURL:   DefaultOllamaURL,
	}
}

func GenerateSalt() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func GetConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".convergence"), nil
}

func configPath() (string, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// Exists reports whether a saved config file is present.
func Exists() (bool, error) {
	path, err := configPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// LoadConfig returns the saved config, or defaults when none exists.
// A config that exists but cannot be parsed is an error (never silently replaced).
func LoadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if err := cfg.ValidateComposeProvider(); err != nil {
		return nil, fmt.Errorf("validating config %s: %w", path, err)
	}
	if err := ValidateExtProcTimeout(cfg.ExtProcTimeout); err != nil {
		return nil, fmt.Errorf("validating config %s: %w", path, err)
	}
	if err := cfg.ValidateSkillScan(); err != nil {
		return nil, fmt.Errorf("validating config %s: %w", path, err)
	}
	if cfg.StandinSalt == "" {
		if cfg.StandinSalt, err = GenerateSalt(); err != nil {
			return nil, fmt.Errorf("generating salt: %w", err)
		}
	}
	if cfg.GatewayPort == 0 {
		cfg.GatewayPort = DefaultGatewayPort
	}
	if cfg.BindAddress == "" {
		cfg.BindAddress = DefaultBindAddress
	}
	if cfg.OptSchedule == "" {
		cfg.OptSchedule = DefaultOptSchedule
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	dir, err := GetConfigDir()
	if err != nil {
		return err
	}
	// 0755: the directory also holds non-secret files bind-mounted into
	// containers; config.yaml and .env themselves stay 0600.
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: dir holds non-secret files bind-mounted into containers (see comment)
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("serializing config: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o600)
}
