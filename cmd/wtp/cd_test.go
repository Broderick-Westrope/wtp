package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/fzf"
)

// --- test doubles ---

// stubExecutor returns a fixed ExecutionResult.
type stubExecutor struct {
	output string
}

func (s *stubExecutor) Execute(_ []command.Command) (*command.ExecutionResult, error) {
	return &command.ExecutionResult{
		Results: []command.Result{{Output: s.output}},
	}, nil
}

// stubFinder is a mock fzf.Finder for testing.
type stubFinder struct {
	available bool
	result    string
	err       error
	findCalls []stubFindCall
}

type stubFindCall struct {
	items []string
	query string
}

func (m *stubFinder) Available() bool { return m.available }

func (m *stubFinder) Find(items []string, query string) (string, error) {
	m.findCalls = append(m.findCalls, stubFindCall{items: items, query: query})
	return m.result, m.err
}

// --- worktree list fixtures ---

const realisticWorktreeList = `worktree /Users/dev/project/main
HEAD abc123
branch refs/heads/main

worktree /Users/dev/project/worktrees/feature/auth
HEAD def456
branch refs/heads/feature/auth

`

// ===== Critical User Scenarios =====

// This is the most important test - the core value proposition
func TestCdCommand_AlwaysOutputsAbsolutePath(t *testing.T) {
	tests := []struct {
		name          string
		worktreeName  string
		expectedPath  string
		shouldSucceed bool
	}{
		{
			name:          "main worktree by @ symbol",
			worktreeName:  "@",
			expectedPath:  "/Users/dev/project/main",
			shouldSucceed: true,
		},
		{
			name:          "feature worktree by branch name",
			worktreeName:  "feature/auth",
			expectedPath:  "/Users/dev/project/worktrees/feature/auth",
			shouldSucceed: true,
		},
		{
			name:          "directory name alone does not match (branch-name resolution only)",
			worktreeName:  "auth",
			shouldSucceed: false, // "auth" is not a branch name; branch is "feature/auth"
		},
		{
			name:          "nonexistent worktree",
			worktreeName:  "nonexistent",
			shouldSucceed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktrees := parseWorktreesFromOutput(realisticWorktreeList)

			resolvedPath, err := resolveWorktreePathByName(tt.worktreeName, worktrees)

			if tt.shouldSucceed {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedPath, resolvedPath,
					"cd command must output correct absolute path")
				assert.True(t, filepath.IsAbs(resolvedPath),
					"cd command must always output absolute paths")
			} else {
				assert.Error(t, err,
					"cd command should return error for nonexistent worktrees")
			}
		})
	}
}

// Test the architectural guarantee: no environment variable dependency
func TestCdCommand_NoEnvironmentVariableDependency(t *testing.T) {
	originalEnv := os.Getenv("WTP_SHELL_INTEGRATION")
	t.Cleanup(func() {
		if originalEnv != "" {
			require.NoError(t, os.Setenv("WTP_SHELL_INTEGRATION", originalEnv))
		} else {
			require.NoError(t, os.Unsetenv("WTP_SHELL_INTEGRATION"))
		}
	})

	envStates := []struct {
		name  string
		value string
	}{
		{"no env var", ""},
		{"env var set to 1", "1"},
		{"env var set to 0", "0"},
		{"env var set to random", "random"},
	}

	for _, env := range envStates {
		t.Run(env.name, func(t *testing.T) {
			if env.value == "" {
				require.NoError(t, os.Unsetenv("WTP_SHELL_INTEGRATION"))
			} else {
				require.NoError(t, os.Setenv("WTP_SHELL_INTEGRATION", env.value))
			}

			worktreeList := "worktree /test/main\nHEAD abc\nbranch refs/heads/main\n\n"
			worktrees := parseWorktreesFromOutput(worktreeList)

			resolvedPath, err := resolveWorktreePathByName("@", worktrees)
			require.NoError(t, err)
			assert.Equal(t, "/test/main", resolvedPath,
				"Path resolution must not depend on environment variables")
		})
	}
}

// Test edge cases that could break in production
func TestCdCommand_EdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		worktreeName string
		worktreeList string
		expected     string
		shouldFind   bool
	}{
		{
			name:         "worktree name with special characters",
			worktreeName: "feature/fix-auth-123",
			worktreeList: "worktree /path/feature-fix-auth-123\nHEAD abc\nbranch refs/heads/feature/fix-auth-123\n\n",
			expected:     "/path/feature-fix-auth-123",
			shouldFind:   true,
		},
		{
			name:         "completion marker removal (asterisk)",
			worktreeName: "feature*",
			worktreeList: "worktree /path/feature\nHEAD abc\nbranch refs/heads/feature\n\n",
			expected:     "/path/feature",
			shouldFind:   true,
		},
		{
			name:         "empty worktree list",
			worktreeName: "any",
			worktreeList: "",
			expected:     "",
			shouldFind:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktrees := parseWorktreesFromOutput(tt.worktreeList)

			result, err := resolveWorktreePathByName(tt.worktreeName, worktrees)

			if tt.shouldFind {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// Only test command structure that affects user behavior
func TestCdCommand_CoreBehavior(t *testing.T) {
	cmd := NewCdCommand()
	assert.Equal(t, "cd", cmd.Name)
	assert.Equal(t, "Output absolute path to worktree", cmd.Usage)
	assert.NotNil(t, cmd.ShellComplete)
}

func TestGetWorktreesForCd(t *testing.T) {
	RunWriterCommonTests(t, "getWorktreesForCd", getWorktreesForCd)
}

func TestCompleteWorktreesForCd(t *testing.T) {
	t.Run("should not panic when called", func(t *testing.T) {
		cmd := &cli.Command{}

		// Should not panic even without proper git setup
		assert.NotPanics(t, func() {
			restore := silenceStdout(t)
			defer restore()

			completeWorktreesForCd(context.Background(), cmd)
		})
	})
}

// ===== fzf integration =====

func TestCdCommand_FzfInteractivePicker(t *testing.T) {
	t.Run("no args with fzf available launches picker", func(t *testing.T) {
		finder := &stubFinder{available: true, result: "feature/auth"}
		exec := &stubExecutor{output: realisticWorktreeList}

		var buf bytes.Buffer
		err := cdCommandWithCommandExecutor(&buf, exec, "", finder)

		require.NoError(t, err)
		assert.Equal(t, "/Users/dev/project/worktrees/feature/auth\n", buf.String())
		require.Len(t, finder.findCalls, 1)
		assert.Empty(t, finder.findCalls[0].query, "picker should have no initial query")
		assert.ElementsMatch(t, []string{"@", "feature/auth"}, finder.findCalls[0].items)
	})

	t.Run("no args with fzf selects main via @", func(t *testing.T) {
		finder := &stubFinder{available: true, result: "@"}
		exec := &stubExecutor{output: realisticWorktreeList}

		var buf bytes.Buffer
		err := cdCommandWithCommandExecutor(&buf, exec, "", finder)

		require.NoError(t, err)
		assert.Equal(t, "/Users/dev/project/main\n", buf.String())
	})

	t.Run("no args with fzf canceled produces no output", func(t *testing.T) {
		finder := &stubFinder{available: true, err: fzf.ErrCanceled}
		exec := &stubExecutor{output: realisticWorktreeList}

		var buf bytes.Buffer
		err := cdCommandWithCommandExecutor(&buf, exec, "", finder)

		require.NoError(t, err)
		assert.Empty(t, buf.String(), "canceled fzf should produce no output")
	})

	t.Run("no args without fzf falls back to main worktree", func(t *testing.T) {
		finder := &stubFinder{available: false}
		exec := &stubExecutor{output: realisticWorktreeList}

		var buf bytes.Buffer
		err := cdCommandWithCommandExecutor(&buf, exec, "", finder)

		require.NoError(t, err)
		assert.Equal(t, "/Users/dev/project/main\n", buf.String())
		assert.Empty(t, finder.findCalls, "should not call fzf when unavailable")
	})

	t.Run("no args with nil finder falls back to main worktree", func(t *testing.T) {
		exec := &stubExecutor{output: realisticWorktreeList}

		var buf bytes.Buffer
		err := cdCommandWithCommandExecutor(&buf, exec, "", nil)

		require.NoError(t, err)
		assert.Equal(t, "/Users/dev/project/main\n", buf.String())
	})
}

func TestCdCommand_FzfFuzzyFallback(t *testing.T) {
	t.Run("exact match bypasses fzf", func(t *testing.T) {
		finder := &stubFinder{available: true}
		exec := &stubExecutor{output: realisticWorktreeList}

		var buf bytes.Buffer
		err := cdCommandWithCommandExecutor(&buf, exec, "feature/auth", finder)

		require.NoError(t, err)
		assert.Equal(t, "/Users/dev/project/worktrees/feature/auth\n", buf.String())
		assert.Empty(t, finder.findCalls, "fzf should not be called for exact matches")
	})

	t.Run("no exact match falls back to fzf with query", func(t *testing.T) {
		finder := &stubFinder{available: true, result: "feature/auth"}
		exec := &stubExecutor{output: realisticWorktreeList}

		var buf bytes.Buffer
		err := cdCommandWithCommandExecutor(&buf, exec, "auth", finder)

		require.NoError(t, err)
		assert.Equal(t, "/Users/dev/project/worktrees/feature/auth\n", buf.String())
		require.Len(t, finder.findCalls, 1)
		assert.Equal(t, "auth", finder.findCalls[0].query, "fzf should receive the original query")
	})

	t.Run("no exact match and fzf canceled produces no output", func(t *testing.T) {
		finder := &stubFinder{available: true, err: fzf.ErrCanceled}
		exec := &stubExecutor{output: realisticWorktreeList}

		var buf bytes.Buffer
		err := cdCommandWithCommandExecutor(&buf, exec, "nonexistent", finder)

		require.NoError(t, err)
		assert.Empty(t, buf.String())
	})

	t.Run("no exact match without fzf returns error", func(t *testing.T) {
		finder := &stubFinder{available: false}
		exec := &stubExecutor{output: realisticWorktreeList}

		var buf bytes.Buffer
		err := cdCommandWithCommandExecutor(&buf, exec, "nonexistent", finder)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "nonexistent")
	})

	t.Run("no exact match with nil finder returns error", func(t *testing.T) {
		exec := &stubExecutor{output: realisticWorktreeList}

		var buf bytes.Buffer
		err := cdCommandWithCommandExecutor(&buf, exec, "nonexistent", nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "nonexistent")
	})
}
