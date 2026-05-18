package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_NonExistentFile(t *testing.T) {
	tempDir := t.TempDir()

	config, err := LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if config.HasHooks() {
		t.Error("Expected no hooks in default config")
	}
}

func TestLoadConfig_ValidFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, ConfigFileName)

	configContent := `version: "1.0"
defaults:
  base_dir: "../my-worktrees"
hooks:
  post_create:
    - type: copy
      from: ".env.example"
      to: ".env"
    - type: command
      command: "echo test"
    - type: symlink
      from: ".bin"
      to: ".bin"
`

	err := os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	config, err := LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// version and defaults are ignored gracefully
	if len(config.Hooks.PostCreate) != 3 {
		t.Errorf("Expected 3 hooks, got %d", len(config.Hooks.PostCreate))
	}

	if config.Hooks.PostCreate[0].Type != HookTypeCopy {
		t.Errorf("Expected first hook type 'copy', got %s", config.Hooks.PostCreate[0].Type)
	}

	if config.Hooks.PostCreate[1].Type != HookTypeCommand {
		t.Errorf("Expected second hook type 'command', got %s", config.Hooks.PostCreate[1].Type)
	}

	if config.Hooks.PostCreate[2].Type != HookTypeSymlink {
		t.Errorf("Expected third hook type 'symlink', got %s", config.Hooks.PostCreate[2].Type)
	}
}

func TestLoadConfig_CopyHookDefaultsToFrom(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, ConfigFileName)

	configContent := `hooks:
  post_create:
    - type: copy
      from: ".env"
`

	err := os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	config, err := LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(config.Hooks.PostCreate) != 1 {
		t.Fatalf("Expected 1 hook, got %d", len(config.Hooks.PostCreate))
	}

	if got := config.Hooks.PostCreate[0].To; got != ".env" {
		t.Errorf("Expected hook.To to default to %q, got %q", ".env", got)
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, ConfigFileName)

	invalidContent := `hooks:
  post_create:
    - type: copy
      from: ".env.example"
      # Invalid YAML syntax
      to: ".env"
    invalid_structure
`

	err := os.WriteFile(configPath, []byte(invalidContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	_, err = LoadConfig(tempDir)
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
	}
}

func TestSaveConfig(t *testing.T) {
	tempDir := t.TempDir()

	config := &Config{
		Hooks: Hooks{
			PostCreate: []Hook{
				{
					Type: HookTypeCopy,
					From: ".env.example",
					To:   ".env",
				},
			},
		},
	}

	err := SaveConfig(tempDir, config)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Verify file was created
	configPath := filepath.Join(tempDir, ConfigFileName)
	if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
		t.Error("Config file was not created")
	}

	// Load it back and verify
	loadedConfig, err := LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load saved config: %v", err)
	}

	if len(loadedConfig.Hooks.PostCreate) != 1 {
		t.Errorf("Expected 1 hook, got %d", len(loadedConfig.Hooks.PostCreate))
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
		expectError bool
	}{
		{
			name: "valid config with hooks",
			config: &Config{
				Hooks: Hooks{
					PostCreate: []Hook{
						{
							Type: HookTypeCopy,
							From: ".env.example",
							To:   ".env",
						},
					},
				},
			},
			expectError: false,
		},
		{
			name:        "empty config",
			config:      &Config{},
			expectError: false,
		},
		{
			name: "invalid copy hook - missing from",
			config: &Config{
				Hooks: Hooks{
					PostCreate: []Hook{
						{
							Type: HookTypeCopy,
							To:   ".env",
						},
					},
				},
			},
			expectError: true,
		},
		{
			name: "invalid command hook - missing command",
			config: &Config{
				Hooks: Hooks{
					PostCreate: []Hook{
						{
							Type: HookTypeCommand,
							// Missing Command field - should cause validation error
						},
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.config.ApplyDefaults()
			err := tt.config.Validate()
			if tt.expectError && err == nil {
				t.Error("Expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestHookValidate(t *testing.T) {
	tests := []struct {
		name        string
		hook        Hook
		expectError bool
	}{
		{
			name: "valid copy hook",
			hook: Hook{
				Type: HookTypeCopy,
				From: ".env.example",
				To:   ".env",
			},
			expectError: false,
		},
		{
			name: "valid command hook",
			hook: Hook{
				Type:    HookTypeCommand,
				Command: "echo test",
			},
			expectError: false,
		},
		{
			name: "valid symlink hook",
			hook: Hook{
				Type: HookTypeSymlink,
				From: ".bin",
				To:   ".bin",
			},
			expectError: false,
		},
		{
			name: "copy hook missing from",
			hook: Hook{
				Type: HookTypeCopy,
				To:   ".env",
			},
			expectError: true,
		},
		{
			name: "copy hook missing to",
			hook: Hook{
				Type: HookTypeCopy,
				From: ".env.example",
			},
			expectError: false,
		},
		{
			name: "copy hook missing to with absolute from",
			hook: Hook{
				Type: HookTypeCopy,
				From: filepath.Join(string(os.PathSeparator), "tmp", "source.txt"),
			},
			expectError: true,
		},
		{
			name: "copy hook with command field",
			hook: Hook{
				Type:    HookTypeCopy,
				From:    ".env.example",
				To:      ".env",
				Command: "echo", // Should not have command
			},
			expectError: true,
		},
		{
			name: "command hook missing command",
			hook: Hook{
				Type: HookTypeCommand,
			},
			expectError: true,
		},
		{
			name: "symlink hook missing from",
			hook: Hook{
				Type: HookTypeSymlink,
				To:   ".bin",
			},
			expectError: true,
		},
		{
			name: "symlink hook missing to",
			hook: Hook{
				Type: HookTypeSymlink,
				From: ".bin",
			},
			expectError: true,
		},
		{
			name: "symlink hook with command field",
			hook: Hook{
				Type:    HookTypeSymlink,
				From:    ".bin",
				To:      ".bin",
				Command: "echo", // Should not have command
			},
			expectError: true,
		},
		{
			name: "command hook with from/to fields",
			hook: Hook{
				Type:    HookTypeCommand,
				Command: "echo",
				From:    ".env.example", // Should not have from/to
				To:      ".env",
			},
			expectError: true,
		},
		{
			name: "invalid hook type",
			hook: Hook{
				Type: "invalid",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.hook.Validate()
			if tt.expectError && err == nil {
				t.Error("Expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestHookValidate_DoesNotMutateTo(t *testing.T) {
	hook := Hook{
		Type: HookTypeCopy,
		From: ".env",
	}

	if err := hook.Validate(); err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	if hook.To != "" {
		t.Errorf("Expected hook.To to remain empty, got %q", hook.To)
	}
}

func TestHookApplyDefaults_CopyToDefaultsToFrom(t *testing.T) {
	hook := Hook{
		Type: HookTypeCopy,
		From: ".env",
	}

	hook.ApplyDefaults()

	if hook.To != hook.From {
		t.Errorf("Expected hook.To to default to %q, got %q", hook.From, hook.To)
	}

	if err := hook.Validate(); err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}
}

func TestConfigApplyDefaults_CopyToDefaultsToFrom(t *testing.T) {
	config := &Config{
		Hooks: Hooks{
			PostCreate: []Hook{
				{
					Type: HookTypeCopy,
					From: ".env",
				},
			},
		},
	}

	config.ApplyDefaults()

	if err := config.Validate(); err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}

	if got := config.Hooks.PostCreate[0].To; got != ".env" {
		t.Errorf("Expected hook.To to default to %q, got %q", ".env", got)
	}
}

func TestConfigValidate_CopyAbsoluteFromRequiresTo(t *testing.T) {
	config := &Config{
		Hooks: Hooks{
			PostCreate: []Hook{
				{
					Type: HookTypeCopy,
					From: filepath.Join(string(os.PathSeparator), "tmp", "source.txt"),
				},
			},
		},
	}

	config.ApplyDefaults()

	if err := config.Validate(); err == nil {
		t.Fatalf("Expected error but got nil")
	}
}

func TestHasHooks(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected bool
	}{
		{
			name: "config with hooks",
			config: &Config{
				Hooks: Hooks{
					PostCreate: []Hook{
						{Type: HookTypeCopy, From: "a", To: "b"},
					},
				},
			},
			expected: true,
		},
		{
			name: "config without hooks",
			config: &Config{
				Hooks: Hooks{},
			},
			expected: false,
		},
		{
			name: "config with empty hooks slice",
			config: &Config{
				Hooks: Hooks{
					PostCreate: []Hook{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.HasHooks()
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
