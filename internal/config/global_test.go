package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	axdg "github.com/adrg/xdg"
)

// setXDGConfigHome overrides XDG_CONFIG_HOME for the duration of the test and
// automatically restores the original value via t.Cleanup.
func setXDGConfigHome(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_CONFIG_HOME", dir)
	axdg.Reload()
}

// TestGlobalLoadDefaults verifies that LoadGlobalConfig returns DefaultCacheTTL
// when the config file does not exist.
func TestGlobalLoadDefaults(t *testing.T) {
	setXDGConfigHome(t, t.TempDir())

	cfg, err := LoadGlobalConfig()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if cfg.CacheTTL != DefaultCacheTTL {
		t.Errorf("expected CacheTTL %v, got %v", DefaultCacheTTL, cfg.CacheTTL)
	}

	if cfg.ArchiveRetention != DefaultArchiveRetention {
		t.Errorf("expected ArchiveRetention %v, got %v", DefaultArchiveRetention, cfg.ArchiveRetention)
	}

	if cfg.MaintenanceInterval != DefaultMaintenanceInterval {
		t.Errorf("expected MaintenanceInterval %v, got %v", DefaultMaintenanceInterval, cfg.MaintenanceInterval)
	}
}

// TestGlobalRoundTrip verifies that SaveGlobalConfig followed by LoadGlobalConfig
// preserves the stored values faithfully.
func TestGlobalRoundTrip(t *testing.T) {
	setXDGConfigHome(t, t.TempDir())

	want := GlobalConfig{
		CacheTTL:            5 * time.Minute,
		ArchiveRetention:    48 * time.Hour,
		MaintenanceInterval: 30 * time.Second,
	}

	if err := SaveGlobalConfig(want); err != nil {
		t.Fatalf("SaveGlobalConfig: %v", err)
	}

	got, err := LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}

	if got.CacheTTL != want.CacheTTL {
		t.Errorf("CacheTTL: want %v, got %v", want.CacheTTL, got.CacheTTL)
	}

	if got.ArchiveRetention != want.ArchiveRetention {
		t.Errorf("ArchiveRetention: want %v, got %v", want.ArchiveRetention, got.ArchiveRetention)
	}

	if got.MaintenanceInterval != want.MaintenanceInterval {
		t.Errorf("MaintenanceInterval: want %v, got %v", want.MaintenanceInterval, got.MaintenanceInterval)
	}
}

// TestGlobalEnsureCreatesFile verifies that EnsureGlobalConfig creates the config
// file on first call and then reads it successfully on the second call.
func TestGlobalEnsureCreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	setXDGConfigHome(t, tmpDir)

	configPath := filepath.Join(tmpDir, "wtp", "config.yml")

	// File must not exist before the first call.
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected config file to be absent before EnsureGlobalConfig")
	}

	first, err := EnsureGlobalConfig()
	if err != nil {
		t.Fatalf("first EnsureGlobalConfig: %v", err)
	}

	if first.CacheTTL != DefaultCacheTTL {
		t.Errorf("first call: want CacheTTL %v, got %v", DefaultCacheTTL, first.CacheTTL)
	}

	if first.ArchiveRetention != DefaultArchiveRetention {
		t.Errorf("first call: want ArchiveRetention %v, got %v", DefaultArchiveRetention, first.ArchiveRetention)
	}

	if first.MaintenanceInterval != DefaultMaintenanceInterval {
		t.Errorf("first call: want MaintenanceInterval %v, got %v", DefaultMaintenanceInterval, first.MaintenanceInterval)
	}

	// File must exist now.
	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
		t.Fatalf("expected config file to exist after first EnsureGlobalConfig")
	}

	// Second call should read the file and return the same value.
	second, err := EnsureGlobalConfig()
	if err != nil {
		t.Fatalf("second EnsureGlobalConfig: %v", err)
	}

	if second.CacheTTL != first.CacheTTL {
		t.Errorf("second call: want CacheTTL %v, got %v", first.CacheTTL, second.CacheTTL)
	}
}

// TestGlobalDurationParsing verifies that human-readable duration strings are
// correctly parsed from a YAML config file.
func TestGlobalDurationParsing(t *testing.T) {
	cases := []struct {
		yaml string
		want time.Duration
	}{
		{yaml: `cache_ttl: "60s"`, want: 60 * time.Second},
		{yaml: `cache_ttl: "5m"`, want: 5 * time.Minute},
		{yaml: `cache_ttl: "1h"`, want: time.Hour},
		{yaml: `cache_ttl: 120`, want: 120 * time.Second}, // integer seconds
	}

	for _, tc := range cases {
		t.Run(tc.yaml, func(t *testing.T) {
			tmpDir := t.TempDir()
			setXDGConfigHome(t, tmpDir)

			configPath := filepath.Join(tmpDir, "wtp", "config.yml")
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}

			if err := os.WriteFile(configPath, []byte(tc.yaml+"\n"), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			cfg, err := LoadGlobalConfig()
			if err != nil {
				t.Fatalf("LoadGlobalConfig: %v", err)
			}

			if cfg.CacheTTL != tc.want {
				t.Errorf("want %v, got %v", tc.want, cfg.CacheTTL)
			}
		})
	}
}

// TestGlobalArchiveRetentionParsing verifies that archive_retention is correctly
// parsed as a duration string or integer seconds, with defaults when absent.
func TestGlobalArchiveRetentionParsing(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want time.Duration
	}{
		{name: "duration string 240h", yaml: `archive_retention: "240h"`, want: 240 * time.Hour},
		{name: "integer seconds", yaml: `archive_retention: 864000`, want: 240 * time.Hour},
		{name: "absent uses default", yaml: `cache_ttl: "60s"`, want: DefaultArchiveRetention},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			setXDGConfigHome(t, tmpDir)

			configPath := filepath.Join(tmpDir, "wtp", "config.yml")
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}

			if err := os.WriteFile(configPath, []byte(tc.yaml+"\n"), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			cfg, err := LoadGlobalConfig()
			if err != nil {
				t.Fatalf("LoadGlobalConfig: %v", err)
			}

			if cfg.ArchiveRetention != tc.want {
				t.Errorf("want %v, got %v", tc.want, cfg.ArchiveRetention)
			}
		})
	}
}

// TestGlobalMaintenanceIntervalParsing verifies that maintenance_interval is correctly
// parsed as a duration string, with defaults when absent.
func TestGlobalMaintenanceIntervalParsing(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want time.Duration
	}{
		{name: "duration string 10m", yaml: `maintenance_interval: "10m"`, want: 10 * time.Minute},
		{name: "duration string 30s", yaml: `maintenance_interval: "30s"`, want: 30 * time.Second},
		{name: "integer seconds", yaml: `maintenance_interval: 600`, want: 10 * time.Minute},
		{name: "absent uses default", yaml: `cache_ttl: "60s"`, want: DefaultMaintenanceInterval},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			setXDGConfigHome(t, tmpDir)

			configPath := filepath.Join(tmpDir, "wtp", "config.yml")
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}

			if err := os.WriteFile(configPath, []byte(tc.yaml+"\n"), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			cfg, err := LoadGlobalConfig()
			if err != nil {
				t.Fatalf("LoadGlobalConfig: %v", err)
			}

			if cfg.MaintenanceInterval != tc.want {
				t.Errorf("want %v, got %v", tc.want, cfg.MaintenanceInterval)
			}
		})
	}
}

// TestGlobalInvalidYAML verifies that LoadGlobalConfig returns an error when the
// config file contains malformed YAML.
func TestGlobalInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	setXDGConfigHome(t, tmpDir)

	configPath := filepath.Join(tmpDir, "wtp", "config.yml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write a syntactically invalid YAML file.
	invalidYAML := "cache_ttl: [\ninvalid: yaml"
	if err := os.WriteFile(configPath, []byte(invalidYAML), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := LoadGlobalConfig()
	if err == nil {
		t.Fatal("expected an error for invalid YAML, got nil")
	}
}

// TestGlobalAtomicWrite verifies that a leftover .tmp file does not prevent a
// subsequent successful SaveGlobalConfig.
func TestGlobalAtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	setXDGConfigHome(t, tmpDir)

	configPath := filepath.Join(tmpDir, "wtp", "config.yml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Simulate a leftover temp file from a previous interrupted write.
	tmpFile := configPath + ".tmp"
	if err := os.WriteFile(tmpFile, []byte("leftover"), 0o600); err != nil {
		t.Fatalf("WriteFile (leftover): %v", err)
	}

	cfg := GlobalConfig{CacheTTL: 30 * time.Second}
	if err := SaveGlobalConfig(cfg); err != nil {
		t.Fatalf("SaveGlobalConfig: %v", err)
	}

	// The actual config file must be readable and correct.
	got, err := LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}

	if got.CacheTTL != cfg.CacheTTL {
		t.Errorf("want %v, got %v", cfg.CacheTTL, got.CacheTTL)
	}
}
