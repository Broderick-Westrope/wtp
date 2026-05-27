package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// Helper function
func findSubcommand(cmd *cli.Command, name string) *cli.Command {
	for _, sub := range cmd.Commands {
		if sub.Name == name {
			return sub
		}
	}
	return nil
}

// Focus on what matters: command behavior, not structure
func TestNewHookCommand_SupportedShells(t *testing.T) {
	cmd := NewHookCommand()
	assert.Equal(t, "hook", cmd.Name)

	// What matters: all required shells are supported
	supportedShells := []string{"bash", "zsh", "fish"}
	for _, shell := range supportedShells {
		subCmd := findSubcommand(cmd, shell)
		assert.NotNil(t, subCmd, "Hook command must support %s", shell)
	}
}

func TestHookCommand_GeneratesValidShellScripts(t *testing.T) {
	tests := []struct {
		name     string
		shell    string
		contains []string
	}{
		{
			name:  "bash generates valid hook",
			shell: "bash",
			contains: []string{
				"wtp()",
				"if [[ \"$1\" == \"cd\" ]]",
				"command wtp cd",
				"cd \"$target_dir\"",
				"__WTP_HOOKED=1",
				"__wtp_cd:",
				"mktemp",
			},
		},
		{
			name:  "zsh generates valid hook",
			shell: "zsh",
			contains: []string{
				"wtp()",
				"if [[ \"$1\" == \"cd\" ]]",
				"command wtp cd",
				"cd \"$target_dir\"",
				"__WTP_HOOKED=1",
				"__wtp_cd:",
				"mktemp",
			},
		},
		{
			name:  "fish generates valid hook",
			shell: "fish",
			contains: []string{
				"function wtp",
				"if test \"$argv[1]\" = \"cd\"",
				"command wtp cd",
				"cd \"$target_dir\"",
				"__WTP_HOOKED=1",
				"__wtp_cd:",
				"mktemp",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &cli.Command{
				Commands: []*cli.Command{
					NewHookCommand(),
				},
			}

			var buf bytes.Buffer
			app.Writer = &buf

			ctx := context.Background()
			err := app.Run(ctx, []string{"wtp", "hook", tt.shell})
			assert.NoError(t, err)

			output := buf.String()
			assert.NotEmpty(t, output, "Hook script should not be empty")

			// Essential behavior: script contains required elements
			for _, expected := range tt.contains {
				assert.Contains(t, output, expected)
			}

			// Essential behavior: no legacy environment variable dependency
			assert.NotContains(t, output, "WTP_SHELL_INTEGRATION")
		})
	}
}

// Test the core business logic that matters most
// Test fish variable scoping: target_dir must be declared outside if/else block
func TestFishHook_VariableScoping(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, printFishHook(&buf))
	output := buf.String()

	// target_dir must be declared BEFORE the if/else block (outside block scope)
	// Correct: "set -l target_dir" on its own line, then "set target_dir ..." inside blocks
	assert.Contains(t, output, "set -l target_dir\n",
		"target_dir must be declared outside if/else block for proper fish scoping")

	// Inside if/else blocks, assignment should NOT use -l flag
	assert.Contains(t, output, "set target_dir (command wtp cd 2>/dev/null)",
		"target_dir assignment in if block should not use -l flag")
	assert.Contains(t, output, "set target_dir (command wtp cd $argv[2] 2>/dev/null)",
		"target_dir assignment in else block should not use -l flag")

	// New marker-aware variables use proper -l scoping
	assert.Contains(t, output, "set -l __wtp_stderr_file",
		"__wtp_stderr_file must use -l scoping")
	assert.Contains(t, output, "set -l __wtp_exit",
		"__wtp_exit must use -l scoping")
	assert.Contains(t, output, "set -l __wtp_target",
		"__wtp_target must use -l scoping")
}

func TestHookScripts_HandleEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		shell         string
		requiredLogic []string
		notContains   []string
	}{
		{
			name:  "bash hook supports no-arg cd",
			shell: "bash",
			requiredLogic: []string{
				"if [[ -z \"$2\" ]]",               // No-arg branch
				"target_dir=$(command wtp cd",      // Uses `wtp cd` default behavior
				"target_dir=$(command wtp cd \"$2", // Uses explicit worktree name when present
				"${__wtp_line#__wtp_cd:}",          // Parameter expansion for marker extraction
			},
			notContains: []string{
				"Usage: wtp cd <worktree>",
				"echo \"Usage:",
			},
		},
		{
			name:  "zsh hook supports no-arg cd",
			shell: "zsh",
			requiredLogic: []string{
				"if [[ -z \"$2\" ]]",               // No-arg branch
				"target_dir=$(command wtp cd",      // Uses `wtp cd` default behavior
				"target_dir=$(command wtp cd \"$2", // Uses explicit worktree name when present
				"${__wtp_line#__wtp_cd:}",          // Parameter expansion for marker extraction
			},
			notContains: []string{
				"Usage: wtp cd <worktree>",
				"echo \"Usage:",
			},
		},
		{
			name:  "fish hook supports no-arg cd",
			shell: "fish",
			requiredLogic: []string{
				"if test -z \"$argv[2]\"",                      // No-arg branch
				"set target_dir (command wtp",                  // Uses `wtp cd` (no -l inside block)
				"command wtp cd $argv[2]",                      // Uses explicit worktree name when present
				"cd \"$target_dir\"",                           // Handles spaces safely
				"string replace '__wtp_cd:' '' -- $__wtp_line", // Fish marker extraction
			},
			notContains: []string{
				"Usage: wtp cd <worktree>",
				"echo \"Usage:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			switch tt.shell {
			case "bash":
				require.NoError(t, printBashHook(&buf))
			case "zsh":
				require.NoError(t, printZshHook(&buf))
			case "fish":
				require.NoError(t, printFishHook(&buf))
			}

			output := buf.String()
			for _, logic := range tt.requiredLogic {
				assert.Contains(t, output, logic, "Hook must handle edge cases properly")
			}
			for _, unexpected := range tt.notContains {
				assert.NotContains(t, output, unexpected)
			}
		})
	}
}

func TestHookScripts_PreserveExitCode(t *testing.T) {
	tests := []struct {
		name     string
		printFn  func(w *bytes.Buffer) error
		contains []string
	}{
		{
			name: "bash preserves exit code",
			printFn: func(w *bytes.Buffer) error {
				return printBashHook(w)
			},
			contains: []string{
				"local __wtp_exit=$?",
				"return $__wtp_exit",
			},
		},
		{
			name: "zsh preserves exit code",
			printFn: func(w *bytes.Buffer) error {
				return printZshHook(w)
			},
			contains: []string{
				"local __wtp_exit=$?",
				"return $__wtp_exit",
			},
		},
		{
			name: "fish preserves exit code",
			printFn: func(w *bytes.Buffer) error {
				return printFishHook(w)
			},
			contains: []string{
				"set -l __wtp_exit $status",
				"return $__wtp_exit",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, tt.printFn(&buf))
			output := buf.String()
			for _, expected := range tt.contains {
				assert.Contains(t, output, expected)
			}
		})
	}
}

func TestHookScripts_ValidateDirectoryBeforeCd(t *testing.T) {
	tests := []struct {
		name     string
		printFn  func(w *bytes.Buffer) error
		contains string
	}{
		{
			name: "bash validates directory before cd",
			printFn: func(w *bytes.Buffer) error {
				return printBashHook(w)
			},
			contains: `-d "$__wtp_target"`,
		},
		{
			name: "zsh validates directory before cd",
			printFn: func(w *bytes.Buffer) error {
				return printZshHook(w)
			},
			contains: `-d "$__wtp_target"`,
		},
		{
			name: "fish validates directory before cd",
			printFn: func(w *bytes.Buffer) error {
				return printFishHook(w)
			},
			contains: `-d "$__wtp_target"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, tt.printFn(&buf))
			output := buf.String()
			assert.Contains(t, output, tt.contains)
		})
	}
}
