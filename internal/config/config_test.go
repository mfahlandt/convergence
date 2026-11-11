package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRoundtrip(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	cfg := DefaultConfig()
	cfg.Engine = "podman"
	cfg.Provider = ProviderCopilot
	cfg.CopilotToken = "test_token_123"
	cfg.BindAddress = "0.0.0.0"
	cfg.GatewayPort = 3333

	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if *loaded != *cfg {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", *loaded, *cfg)
	}

	info, err := os.Stat(filepath.Join(tempHome, ".convergence", "config.yaml"))
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected permissions 0600, got %o", perm)
	}
}

func TestLoadConfigFillsDefaultsForOldConfig(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	dir := filepath.Join(tempHome, ".convergence")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("engine: docker\nprovider: direct\nstandin_salt: abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BindAddress != DefaultBindAddress || loaded.GatewayPort != DefaultGatewayPort || loaded.StandinSalt != "abc" {
		t.Errorf("unexpected defaults: %+v", *loaded)
	}
}

func TestLoadConfigRejectsCorruptFile(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	dir := filepath.Join(tempHome, ".convergence")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("engine: [unterminated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected parse error for corrupt config")
	}
}

func TestLoadConfigComposeProvider(t *testing.T) {
	for _, tc := range []struct {
		engine, provider, wantErr string
	}{
		{"podman", "", ""},
		{"podman", "auto", ""},
		{"podman", "docker-compose", ""},
		{"podman", "podman-compose", ""},
		{"docker", "auto", ""},
		{"docker", "podman-compose", "requires engine podman"},
		{"podman", "unknown", "invalid compose_provider"},
	} {
		t.Run(tc.engine+"/"+tc.provider, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			cfg := DefaultConfig()
			cfg.Engine, cfg.ComposeProvider = tc.engine, tc.provider
			if err := SaveConfig(cfg); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadConfig()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if loaded.ComposeProvider != tc.provider {
				t.Fatalf("provider=%q, want %q", loaded.ComposeProvider, tc.provider)
			}
		})
	}
}

func TestPublishedPortsIncludesCopilotOnlyForCopilot(t *testing.T) {
	cfg := DefaultConfig()
	for _, p := range cfg.PublishedPorts() {
		if p.Port == CopilotAPIPort {
			t.Fatal("direct provider must not publish copilot port")
		}
	}
	cfg.Provider = ProviderCopilot
	ports := cfg.PublishedPorts()
	if ports[len(ports)-1].Port != CopilotAPIPort {
		t.Fatalf("copilot provider must publish %d, got %+v", CopilotAPIPort, ports)
	}
}

func TestValidateExtProcTimeout(t *testing.T) {
	for _, ok := range []string{"", "5s", "1m30s", "60s"} {
		if err := ValidateExtProcTimeout(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"abc", "0s", "-1s", "10"} {
		if err := ValidateExtProcTimeout(bad); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
}

func TestValidateSkillScan(t *testing.T) {
	for _, m := range []SkillScanMode{"", SkillScanWarn, SkillScanBlock, SkillScanOff} {
		c := &Config{SkillScan: m}
		if err := c.ValidateSkillScan(); err != nil {
			t.Errorf("%q: %v", m, err)
		}
	}
	if (&Config{}).SkillScanOrDefault() != SkillScanOff {
		t.Error("default must be warn")
	}
	if err := (&Config{SkillScan: "strict"}).ValidateSkillScan(); err == nil {
		t.Error("unknown mode must fail")
	}
}
