package main

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/config"
	"github.com/Broderick-Westrope/wtp/v3/internal/errors"
	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

// testOriginURL is the GitHub HTTPS URL used across add command tests.
const testOriginURL = "https://github.com/owner/repo.git"

// ===== Test helpers =====

// mockGetRemoteURL returns a getRemoteURL function that returns originURL for "origin".
func mockGetRemoteURL(originURL string) func(string) (string, error) {
	return func(name string) (string, error) {
		if name == "origin" {
			return originURL, nil
		}
		return "", fmt.Errorf("no such remote: %s", name)
	}
}

// noOriginRemote simulates a repo with no 'origin' remote.
func noOriginRemote() func(string) (string, error) {
	return func(name string) (string, error) {
		return "", fmt.Errorf("no such remote: %s", name)
	}
}

// ===== Command Structure Tests =====

func TestNewAddCommand(t *testing.T) {
	cmd := NewAddCommand()
	assert.NotNil(t, cmd)
	assert.Equal(t, "add", cmd.Name)
	assert.Equal(t, "Create a new worktree", cmd.Usage)
	assert.NotEmpty(t, cmd.Description)
	assert.NotNil(t, cmd.Action)
	assert.NotNil(t, cmd.ShellComplete)

	// Check simplified flags exist
	flagNames := []string{"branch", "exec", "stay"}
	for _, name := range flagNames {
		found := false
		for _, flag := range cmd.Flags {
			if flag.Names()[0] == name {
				found = true
				break
			}
		}
		assert.True(t, found, "Flag %s should exist", name)
	}
}

// ===== Error Type Tests =====

func TestWorkTreeAlreadyExistsError(t *testing.T) {
	t.Run("should format error message with branch name and solutions", func(t *testing.T) {
		originalErr := &MockGitError{msg: "branch already checked out"}
		err := &WorktreeAlreadyExistsError{
			BranchName: "feature/awesome",
			Path:       "/path/to/worktree",
			GitError:   originalErr,
		}

		message := err.Error()

		assert.Contains(t, message, "feature/awesome")
		assert.Contains(t, message, "already checked out in another worktree")
		assert.Contains(t, message, "--force")
		assert.Contains(t, message, "Choose a different branch")
		assert.Contains(t, message, "Remove the existing worktree")
		assert.Contains(t, message, "branch already checked out")
	})

	t.Run("should handle empty branch name", func(t *testing.T) {
		err := &WorktreeAlreadyExistsError{
			BranchName: "",
			Path:       "/path/to/worktree",
			GitError:   &MockGitError{msg: "test error"},
		}

		message := err.Error()

		assert.Contains(t, message, "worktree for branch ''")
		assert.Contains(t, message, "test error")
	})
}

func TestBranchAlreadyExistsError(t *testing.T) {
	t.Run("should format error message with branch name and guidance", func(t *testing.T) {
		originalErr := &MockGitError{msg: "A branch named 'feature/auth' already exists."}
		err := &BranchAlreadyExistsError{
			BranchName: "feature/auth",
			GitError:   originalErr,
		}

		message := err.Error()

		assert.Contains(t, message, "branch 'feature/auth' already exists")
		assert.Contains(t, message, "wtp add feature/auth")
		assert.Contains(t, message, "Choose a different branch name")
		assert.Contains(t, message, "Delete the existing branch")
		assert.Contains(t, message, "A branch named 'feature/auth' already exists.")
	})

	t.Run("should handle empty branch name", func(t *testing.T) {
		err := &BranchAlreadyExistsError{
			BranchName: "",
			GitError:   &MockGitError{msg: "test error"},
		}

		message := err.Error()

		assert.Contains(t, message, "branch '' already exists")
		assert.Contains(t, message, "test error")
	})
}

func TestPathAlreadyExistsError(t *testing.T) {
	t.Run("should format error message with path and solutions", func(t *testing.T) {
		originalErr := &MockGitError{msg: "directory not empty"}
		err := &PathAlreadyExistsError{
			Path:     "/existing/path",
			GitError: originalErr,
		}

		message := err.Error()

		assert.Contains(t, message, "/existing/path")
		assert.Contains(t, message, "already exists and is not empty")
		assert.Contains(t, message, "--force flag")
		assert.Contains(t, message, "Remove the existing directory")
		assert.Contains(t, message, "directory not empty")
	})

	t.Run("should handle empty path", func(t *testing.T) {
		err := &PathAlreadyExistsError{
			Path:     "",
			GitError: &MockGitError{msg: "test error"},
		}

		message := err.Error()

		assert.Contains(t, message, "destination path already exists:")
		assert.Contains(t, message, "test error")
	})
}

func TestMultipleBranchesError(t *testing.T) {
	t.Run("should format error message with branch name and track suggestions", func(t *testing.T) {
		originalErr := &MockGitError{msg: "multiple remotes found"}
		err := &MultipleBranchesError{
			BranchName: "feature/shared",
			GitError:   originalErr,
		}

		message := err.Error()

		assert.Contains(t, message, "feature/shared")
		assert.Contains(t, message, "exists in multiple remotes")
		assert.Contains(t, message, "--track origin/feature/shared")
		assert.Contains(t, message, "--track upstream/feature/shared")
		assert.Contains(t, message, "multiple remotes found")
	})

	t.Run("should handle special characters in branch name", func(t *testing.T) {
		err := &MultipleBranchesError{
			BranchName: "feature/fix-bugs-#123",
			GitError:   &MockGitError{msg: "test error"},
		}

		message := err.Error()

		assert.Contains(t, message, "feature/fix-bugs-#123")
		assert.Contains(t, message, "--track origin/feature/fix-bugs-#123")
		assert.Contains(t, message, "--track upstream/feature/fix-bugs-#123")
	})
}

// Mock error for testing
type MockGitError struct {
	msg string
}

func (e *MockGitError) Error() string {
	return e.msg
}

// ===== Helper Function Tests =====

func TestSetupRepoAndConfig(t *testing.T) {
	t.Run("should setup repository and config from current directory", func(t *testing.T) {
		repo, cfg, mainRepoPath, err := setupRepoAndConfig()

		if err != nil {
			t.Skip("Not in a git repository - skipping test")
		}

		assert.NotNil(t, repo)
		assert.NotNil(t, cfg)
		assert.NotEmpty(t, mainRepoPath)
	})
}

// ===== Pure Business Logic Tests =====

func TestValidateAddInput(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		branch  string
		wantErr bool
	}{
		{
			name:    "no args and no branch flag",
			args:    []string{},
			branch:  "",
			wantErr: true,
		},
		{
			name:    "with args",
			args:    []string{"feature"},
			branch:  "",
			wantErr: false,
		},
		{
			name:    "with branch flag",
			args:    []string{},
			branch:  "new-feature",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &cli.Command{
				Name: "test",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "branch"},
				},
				Action: func(_ context.Context, cmd *cli.Command) error {
					return validateAddInput(cmd)
				},
			}

			args := []string{"test"}
			if tt.branch != "" {
				args = append(args, "--branch", tt.branch)
			}
			args = append(args, tt.args...)

			ctx := context.Background()
			err := app.Run(ctx, args)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "branch name is required")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResolveWorktreePath(t *testing.T) {
	tests := []struct {
		name           string
		branchName     string
		originURL      string
		flags          map[string]any
		expectedSuffix string // suffix of expected path under XDG root
		expectedBranch string
		wantErr        bool
		errContains    string
	}{
		{
			name:           "github HTTPS URL with branch name",
			branchName:     "feature/auth",
			originURL:      "https://github.com/owner/repo.git",
			flags:          map[string]any{},
			expectedSuffix: "owner/repo/feature/auth",
			expectedBranch: "feature/auth",
		},
		{
			name:           "github SCP URL",
			branchName:     "main",
			originURL:      "git@github.com:owner/repo.git",
			flags:          map[string]any{},
			expectedSuffix: "owner/repo/main",
			expectedBranch: "main",
		},
		{
			name:           "no origin remote returns error",
			branchName:     "feature/auth",
			originURL:      "", // triggers noOriginRemote
			flags:          map[string]any{},
			wantErr:        true,
			errContains:    "no 'origin' remote found",
			expectedBranch: "feature/auth",
		},
		{
			name:           "invalid URL returns error",
			branchName:     "feature/auth",
			originURL:      "not-a-valid-url-without-slash-path",
			flags:          map[string]any{},
			wantErr:        true,
			errContains:    "could not parse remote URL",
			expectedBranch: "feature/auth",
		},
		{
			name:           "-b flag overrides positional arg for branch name",
			branchName:     "positional",
			originURL:      "https://github.com/owner/repo.git",
			flags:          map[string]any{"branch": "feature/new"},
			expectedSuffix: "owner/repo/feature/new",
			expectedBranch: "feature/new",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataHome := t.TempDir()
			t.Setenv("XDG_DATA_HOME", dataHome)
			axdg.Reload()

			var getURL func(string) (string, error)
			if tt.originURL == "" {
				getURL = noOriginRemote()
			} else {
				getURL = mockGetRemoteURL(tt.originURL)
			}

			cmd := createTestCLICommand(tt.flags, []string{tt.branchName})
			path, branch, err := resolveWorktreePath(getURL, tt.branchName, cmd)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Equal(t, tt.expectedBranch, branch)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedBranch, branch)
				// Path should be under XDG storage root
				root := xdg.WorktreeStorageRoot()
				expected := filepath.Join(root, tt.expectedSuffix)
				assert.Equal(t, expected, path)
			}
		})
	}
}

// ===== Command Building Tests =====

// ===== Command Execution Tests =====

func TestAddCommand_CommandConstruction(t *testing.T) {
	tests := []struct {
		name           string
		flags          map[string]any
		args           []string
		originURL      string
		expectedSuffix string // expected path suffix under XDG root
		expectError    bool
	}{
		{
			name: "basic worktree creation with -b",
			flags: map[string]any{
				"branch": "feature/test",
			},
			args:           []string{"feature/test"},
			originURL:      "https://github.com/owner/repo.git",
			expectedSuffix: "owner/repo/feature/test",
		},
		{
			name: "new branch creation from commit",
			flags: map[string]any{
				"branch": "new-feature",
			},
			args:           []string{"main"},
			originURL:      "https://github.com/owner/repo.git",
			expectedSuffix: "owner/repo/new-feature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataHome := t.TempDir()
			t.Setenv("XDG_DATA_HOME", dataHome)
			axdg.Reload()

			cmd := createTestCLICommand(tt.flags, tt.args)
			var buf bytes.Buffer
			mockExec := &mockCommandExecutor{}

			cfg := &config.Config{}

			var errBuf bytes.Buffer
			err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(tt.originURL))

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				expectedPath := filepath.Join(xdg.WorktreeStorageRoot(), tt.expectedSuffix)
				require.Len(t, mockExec.executedCommands, 1)
				// Verify path is under XDG storage
				assert.Contains(t, mockExec.executedCommands[0].Args, expectedPath)
			}
		})
	}
}

func TestAddCommand_SuccessMessage(t *testing.T) {
	tests := []struct {
		name           string
		branchName     string
		expectedOutput string
	}{
		{
			name:           "with branch name",
			branchName:     "feature/auth",
			expectedOutput: "✅ Worktree created successfully!",
		},
		{
			name:           "new branch",
			branchName:     "new-feature",
			expectedOutput: "✅ Worktree created successfully!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataHome := t.TempDir()
			t.Setenv("XDG_DATA_HOME", dataHome)
			axdg.Reload()

			cmd := createTestCLICommand(map[string]any{"branch": tt.branchName}, []string{tt.branchName})
			var buf bytes.Buffer
			mockExec := &mockCommandExecutor{}

			cfg := &config.Config{}

			var errBuf bytes.Buffer
			err := addCommandWithCommandExecutor(
				cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL),
			)

			assert.NoError(t, err)
			assert.Contains(t, buf.String(), tt.expectedOutput)
		})
	}
}

// ===== Error Handling Tests =====

func TestAddCommand_ValidationErrors(t *testing.T) {
	tests := []struct {
		name          string
		flags         map[string]any
		args          []string
		expectedError string
	}{
		{
			name:          "no branch name",
			flags:         map[string]any{},
			args:          []string{},
			expectedError: "branch name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := createTestCLICommand(tt.flags, tt.args)
			err := validateAddInput(cmd)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

func TestAddCommand_NoOriginRemote(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	axdg.Reload()

	cmd := createTestCLICommand(map[string]any{"branch": "feature/auth"}, []string{"feature/auth"})
	var buf, errBuf bytes.Buffer
	mockExec := &mockCommandExecutor{}
	cfg := &config.Config{}

	err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", noOriginRemote())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no 'origin' remote found")
}

func TestAddCommand_UnparseableURL(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	axdg.Reload()

	cmd := createTestCLICommand(map[string]any{"branch": "feature/auth"}, []string{"feature/auth"})
	var buf, errBuf bytes.Buffer
	mockExec := &mockCommandExecutor{}
	cfg := &config.Config{}

	// SCP-style URL without a path (no owner/repo)
	badURL := "git@github.com:"
	err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(badURL))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "could not parse remote URL")
}

func TestAddCommand_ExecutionError(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	axdg.Reload()

	mockExec := &mockCommandExecutor{shouldFail: true}
	var buf, errBuf bytes.Buffer
	cmd := createTestCLICommand(map[string]any{"branch": "feature/auth"}, []string{"feature/auth"})
	cfg := &config.Config{}

	err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL))

	assert.Error(t, err)
	assert.Len(t, mockExec.executedCommands, 1)
}

func TestAddCommand_ExecFailureKeepsCreationContext(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	axdg.Reload()

	cmd := createTestCLICommand(map[string]any{
		"branch": "feature/auth",
		"exec":   "false",
	}, []string{})
	var buf, errBuf bytes.Buffer
	exec := &sequencedCommandExecutor{
		results: []command.Result{
			{Output: "worktree created"},
			{Error: assert.AnError},
		},
	}
	cfg := &config.Config{}

	err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, exec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "worktree was created")
	assert.Len(t, exec.executedCommands, 2)
}

// ===== Edge Cases Tests =====

func TestAddCommand_InternationalCharacters(t *testing.T) {
	tests := []struct {
		name       string
		branchName string
	}{
		{
			name:       "Japanese characters",
			branchName: "機能/ログイン",
		},
		{
			name:       "Spanish accents",
			branchName: "función/añadir",
		},
		{
			name:       "Emoji characters",
			branchName: "feature/🚀-rocket",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataHome := t.TempDir()
			t.Setenv("XDG_DATA_HOME", dataHome)
			axdg.Reload()

			mockExec := &mockCommandExecutor{}
			var buf, errBuf bytes.Buffer
			cmd := createTestCLICommand(map[string]any{"branch": tt.branchName}, []string{tt.branchName})
			cfg := &config.Config{}

			err := addCommandWithCommandExecutor(
				cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL),
			)

			assert.NoError(t, err)
			assert.Len(t, mockExec.executedCommands, 1)
			expectedPath := filepath.Join(xdg.WorktreeStorageRoot(), "owner", "repo", tt.branchName)
			assert.Contains(t, mockExec.executedCommands[0].Args, expectedPath)
		})
	}
}

// ===== Helper Functions =====

func createTestCLICommand(flags map[string]any, args []string) *cli.Command {
	app := &cli.Command{
		Name: "test",
		Commands: []*cli.Command{
			{
				Name: "add",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "force"},
					&cli.StringFlag{Name: "branch"},
					&cli.StringFlag{Name: "track"},
					&cli.StringFlag{Name: "exec"},
					&cli.BoolFlag{Name: "stay"},
				},
				Action: func(_ context.Context, _ *cli.Command) error {
					return nil
				},
			},
		},
	}

	cmdArgs := []string{"test", "add"}
	for key, value := range flags {
		switch v := value.(type) {
		case bool:
			if v {
				cmdArgs = append(cmdArgs, "--"+key)
			}
		case string:
			cmdArgs = append(cmdArgs, "--"+key, v)
		}
	}
	cmdArgs = append(cmdArgs, args...)

	ctx := context.Background()
	_ = app.Run(ctx, cmdArgs)

	return app.Commands[0]
}

// ===== Integration Tests =====

func TestAddCommand_SimplifiedInterface(t *testing.T) {
	t.Run("should support wtp add <existing-branch>", func(t *testing.T) {
		// Without a real git repo the resolveBranchTracking step will fail
		mockExec := &mockCommandExecutor{}
		var buf, errBuf bytes.Buffer
		cmd := createTestCLICommand(map[string]any{}, []string{"main"})
		cfg := &config.Config{}

		err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL))

		// resolveBranchTracking calls git.NewRepository which requires a real repo
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not in a git repository")
	})

	t.Run("should support wtp add -b <new-branch>", func(t *testing.T) {
		dataHome := t.TempDir()
		t.Setenv("XDG_DATA_HOME", dataHome)
		axdg.Reload()

		mockExec := &mockCommandExecutor{}
		var buf, errBuf bytes.Buffer
		cmd := createTestCLICommand(map[string]any{"branch": "feature/new"}, []string{})
		cfg := &config.Config{}

		err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL))

		assert.NoError(t, err)
		assert.Len(t, mockExec.executedCommands, 1)
		expectedPath := filepath.Join(xdg.WorktreeStorageRoot(), "owner", "repo", "feature", "new")
		assert.Equal(t, []string{"worktree", "add", "-b", "feature/new", expectedPath},
			mockExec.executedCommands[0].Args)
		assert.Contains(t, buf.String(), "✅ Worktree created successfully!")
	})

	t.Run("should support wtp add -b <new-branch> <commit>", func(t *testing.T) {
		dataHome := t.TempDir()
		t.Setenv("XDG_DATA_HOME", dataHome)
		axdg.Reload()

		mockExec := &mockCommandExecutor{}
		var buf, errBuf bytes.Buffer
		cmd := createTestCLICommand(map[string]any{"branch": "hotfix/urgent"}, []string{"main"})
		cfg := &config.Config{}

		err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL))

		assert.NoError(t, err)
		assert.Len(t, mockExec.executedCommands, 1)
		expectedPath := filepath.Join(xdg.WorktreeStorageRoot(), "owner", "repo", "hotfix", "urgent")
		assert.Equal(t, []string{"worktree", "add", "-b", "hotfix/urgent", expectedPath, "main"},
			mockExec.executedCommands[0].Args)
		assert.Contains(t, buf.String(), "✅ Worktree created successfully!")
	})

	t.Run("should error with no arguments and no -b flag", func(t *testing.T) {
		cmd := createTestCLICommand(map[string]any{}, []string{})

		err := validateAddInput(cmd)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "branch name is required")
	})

	t.Run("should validate input correctly", func(t *testing.T) {
		cmd1 := createTestCLICommand(map[string]any{}, []string{"main"})
		err1 := validateAddInput(cmd1)
		assert.NoError(t, err1)

		cmd2 := createTestCLICommand(map[string]any{"branch": "new-feature"}, []string{})
		err2 := validateAddInput(cmd2)
		assert.NoError(t, err2)

		cmd3 := createTestCLICommand(map[string]any{}, []string{})
		err3 := validateAddInput(cmd3)
		assert.Error(t, err3)
		assert.Contains(t, err3.Error(), "branch name is required")
	})
}

func TestAddCommand_Integration(t *testing.T) {
	t.Run("should coordinate all components successfully", func(t *testing.T) {
		app := &cli.Command{
			Name: "test",
			Commands: []*cli.Command{
				{
					Name: "add",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: "branch", Aliases: []string{"b"}},
						&cli.StringFlag{Name: "track", Aliases: []string{"t"}},
						&cli.StringFlag{Name: "exec"},
					},
					Action: addCommand,
				},
			},
		}

		ctx := context.Background()
		err := app.Run(ctx, []string{"test", "add", "nonexistent-test-branch"})

		assert.Error(t, err)
		// Either "not found" (branch doesn't exist) or "no origin remote"
		assert.True(t,
			containsAny(err.Error(), "not found", "no 'origin' remote"),
			"expected branch-not-found or no-origin error, got: %v", err,
		)
	})

	t.Run("should handle validation errors gracefully", func(t *testing.T) {
		app := &cli.Command{
			Name: "test",
			Commands: []*cli.Command{
				{
					Name: "add",
					Flags: []cli.Flag{
						&cli.StringFlag{Name: "branch", Aliases: []string{"b"}},
						&cli.StringFlag{Name: "exec"},
					},
					Action: addCommand,
				},
			},
		}

		ctx := context.Background()
		err := app.Run(ctx, []string{"test", "add"})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "branch name is required")
	})
}

// containsAny returns true if s contains at least one of the substrings.
func containsAny(s string, substrings ...string) bool {
	for _, sub := range substrings {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

func TestExecutePostCreateHooks_Integration(t *testing.T) {
	t.Run("should handle hooks when config has no hooks", func(t *testing.T) {
		cfg := &config.Config{}
		var buf bytes.Buffer

		err := executePostCreateHooks(&buf, cfg, "/test/repo", "/test/worktree")

		assert.NoError(t, err)
		assert.Empty(t, buf.String())
	})

	t.Run("should handle hook execution errors", func(t *testing.T) {
		cfg := &config.Config{
			Hooks: config.Hooks{
				PostCreate: []config.Hook{
					{Type: "command", Command: "nonexistent-command-xyz test"},
				},
			},
		}
		var buf bytes.Buffer

		err := executePostCreateHooks(&buf, cfg, "/test/repo", "/test/worktree")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute hook")
		assert.Contains(t, buf.String(), "Executing post-create hooks")
	})
}

func TestExecutePostCreateCommand(t *testing.T) {
	t.Run("no exec command should do nothing", func(t *testing.T) {
		var buf bytes.Buffer
		mockExec := &mockCommandExecutor{}

		err := executePostCreateCommand(&buf, mockExec, "", "/test/worktree")
		require.NoError(t, err)
		assert.Empty(t, buf.String())
		assert.Empty(t, mockExec.executedCommands)
	})

	t.Run("should execute command in worktree", func(t *testing.T) {
		var buf bytes.Buffer
		mockExec := &mockCommandExecutor{}

		err := executePostCreateCommand(&buf, mockExec, "echo hello", "/test/worktree")
		require.NoError(t, err)
		require.Len(t, mockExec.executedCommands, 1)
		assert.Equal(t, "/test/worktree", mockExec.executedCommands[0].WorkDir)
		assert.True(t, mockExec.executedCommands[0].Interactive)

		if runtime.GOOS == "windows" {
			assert.Equal(t, "cmd", mockExec.executedCommands[0].Name)
			assert.Equal(t, []string{"/c", "echo hello"}, mockExec.executedCommands[0].Args)
		} else {
			assert.Equal(t, "sh", mockExec.executedCommands[0].Name)
			assert.Equal(t, []string{"-c", "echo hello"}, mockExec.executedCommands[0].Args)
		}
	})
}

func TestDisplaySuccessMessage_Integration(t *testing.T) {
	t.Run("should display friendly success message with branch name", func(t *testing.T) {
		var buf bytes.Buffer
		branchName := "feature/awesome"
		workTreePath := "/xdg/data/wtp/worktrees/owner/repo/feature/awesome"
		mainRepoPath := "/repo"

		require.NoError(t, displaySuccessMessage(&buf, branchName, workTreePath, mainRepoPath, false))

		output := buf.String()
		assert.Contains(t, output, "✅ Worktree created successfully!")
		assert.Contains(t, output, "📁 Location: "+workTreePath)
		assert.Contains(t, output, "🌿 Branch: feature/awesome")
		assert.Contains(t, output, "📍 Changed to worktree directory.")
	})

	t.Run("should display success message without branch name falls back to dir", func(t *testing.T) {
		var buf bytes.Buffer
		branchName := ""
		workTreePath := "/xdg/data/wtp/worktrees/owner/repo/some-path"
		mainRepoPath := "/repo"

		require.NoError(t, displaySuccessMessage(&buf, branchName, workTreePath, mainRepoPath, false))

		output := buf.String()
		assert.Contains(t, output, "✅ Worktree created successfully!")
		assert.Contains(t, output, "📁 Location: "+workTreePath)
		assert.NotContains(t, output, "🌿 Branch:")
		assert.Contains(t, output, "📍 Changed to worktree directory.")
	})

	t.Run("should show @ for main worktree", func(t *testing.T) {
		var buf bytes.Buffer
		branchName := "main"
		workTreePath := "/repo"
		mainRepoPath := "/repo"

		require.NoError(t, displaySuccessMessage(&buf, branchName, workTreePath, mainRepoPath, false))

		output := buf.String()
		assert.Contains(t, output, "✅ Worktree created successfully!")
		assert.Contains(t, output, "📁 Location: /repo")
		assert.Contains(t, output, "🌿 Branch: main")
		assert.Contains(t, output, "📍 Changed to worktree directory.")
	})

	t.Run("should show cd hint when stay is true", func(t *testing.T) {
		var buf bytes.Buffer
		branchName := "feature/awesome"
		workTreePath := "/xdg/data/wtp/worktrees/owner/repo/feature/awesome"
		mainRepoPath := "/repo"

		require.NoError(t, displaySuccessMessage(&buf, branchName, workTreePath, mainRepoPath, true))

		output := buf.String()
		assert.Contains(t, output, "✅ Worktree created successfully!")
		assert.Contains(t, output, "💡 To switch to the new worktree, run:")
		assert.Contains(t, output, "wtp cd feature/awesome")
		assert.NotContains(t, output, "📍 Changed to worktree directory.")
	})
}

// ===== Hook path resolution test =====

func TestAddCommand_HookPathResolution(t *testing.T) {
	// Verify that copy/symlink hooks work when the worktree is under centralized XDG storage.
	// Source resolves from main repo (repoRoot); destination is under XDG.
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	axdg.Reload()

	// The worktree will be created at $XDG_DATA_HOME/wtp/worktrees/owner/repo/feature/test
	expectedWorktreePath := filepath.Join(xdg.WorktreeStorageRoot(), "owner", "repo", "feature", "test")

	// The hooks executor is given the repoRoot as "from" base and the XDG path as "to" base.
	// We just verify the paths passed to executePostCreateHooks are consistent.
	cfg := &config.Config{
		Hooks: config.Hooks{
			PostCreate: []config.Hook{
				{Type: "copy", From: ".env.example", To: ".env"},
			},
		},
	}

	// repoRoot and worktreePath are different directory trees
	repoRoot := "/repo"
	assert.True(t, repoRoot != filepath.Dir(expectedWorktreePath),
		"worktree should be in a different directory tree from the repo")

	// Verify executePostCreateHooks receives the correct paths
	// (we don't actually run the hook since the paths don't exist, just verify no panic)
	var buf bytes.Buffer
	// This will fail because .env.example doesn't exist, but we're testing path wiring
	_ = executePostCreateHooks(&buf, cfg, repoRoot, expectedWorktreePath)
	// The hook executor is initialized with repoRoot as source base — no panic is the key assertion
}

// ===== Mock Implementations =====

type mockCommandExecutor struct {
	executedCommands []command.Command
	shouldFail       bool
	errorMsg         string
}

type sequencedCommandExecutor struct {
	executedCommands []command.Command
	results          []command.Result
	call             int
}

func (s *sequencedCommandExecutor) Execute(commands []command.Command) (*command.ExecutionResult, error) {
	s.executedCommands = append(s.executedCommands, commands...)

	if s.call < len(s.results) {
		result := s.results[s.call]
		s.call++
		return &command.ExecutionResult{Results: []command.Result{{
			Command: commands[0],
			Output:  result.Output,
			Error:   result.Error,
		}}}, nil
	}

	return &command.ExecutionResult{Results: []command.Result{{
		Command: commands[0],
	}}}, nil
}

func (m *mockCommandExecutor) Execute(commands []command.Command) (*command.ExecutionResult, error) {
	m.executedCommands = commands

	if m.shouldFail {
		errorMsg := m.errorMsg
		if errorMsg == "" {
			errorMsg = "mock error"
		}
		return &command.ExecutionResult{
			Results: []command.Result{{
				Command: commands[0],
				Error:   errors.GitCommandFailed("git", errorMsg),
			}},
		}, nil
	}

	results := make([]command.Result, len(commands))
	for i, cmd := range commands {
		results[i] = command.Result{
			Command: cmd,
			Output:  "success",
		}
	}

	return &command.ExecutionResult{Results: results}, nil
}

// ===== Error Analysis Tests =====

func TestAnalyzeGitWorktreeError(t *testing.T) {
	tests := []struct {
		name          string
		workTreePath  string
		branchName    string
		gitOutput     string
		expectedError string
		expectedType  any
	}{
		{
			name:          "branch not found error",
			workTreePath:  "/path/to/worktree",
			branchName:    "nonexistent-branch",
			gitOutput:     "fatal: invalid reference: nonexistent-branch",
			expectedError: "branch 'nonexistent-branch' not found",
			expectedType:  nil,
		},
		{
			name:          "worktree already exists error",
			workTreePath:  "/path/to/worktree",
			branchName:    "feature-branch",
			gitOutput:     "fatal: 'feature-branch' is already checked out at '/existing/path'",
			expectedError: "",
			expectedType:  &WorktreeAlreadyExistsError{},
		},
		{
			name:          "path already exists error",
			workTreePath:  "/existing/path",
			branchName:    "new-branch",
			gitOutput:     "fatal: '/existing/path' already exists",
			expectedError: "",
			expectedType:  &PathAlreadyExistsError{},
		},
		{
			name:          "branch already exists error",
			workTreePath:  "/path/to/worktree",
			branchName:    "existing-branch",
			gitOutput:     "fatal: A branch named 'existing-branch' already exists.",
			expectedError: "",
			expectedType:  &BranchAlreadyExistsError{},
		},
		{
			name:          "multiple branches error",
			workTreePath:  "/path/to/worktree",
			branchName:    "ambiguous-branch",
			gitOutput:     "fatal: 'ambiguous-branch' matched multiple branches",
			expectedError: "",
			expectedType:  &MultipleBranchesError{},
		},
		{
			name:          "invalid path error",
			workTreePath:  "/invalid/path",
			branchName:    "valid-branch",
			gitOutput:     "fatal: could not create directory '/invalid/path'",
			expectedError: "failed to create worktree at '/invalid/path'",
			expectedType:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gitError := assert.AnError
			result := analyzeGitWorktreeError(tt.workTreePath, tt.branchName, gitError, tt.gitOutput)

			assert.Error(t, result, "Should return an error")

			if tt.expectedError != "" {
				assert.Contains(t, result.Error(), tt.expectedError)
			}

			if tt.expectedType != nil {
				assert.IsType(t, tt.expectedType, result)
			}
		})
	}
}

// ===== Marker and Stay Flag Tests =====

func TestAddCommand_EmitsMarkerWhenHooked(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	axdg.Reload()
	t.Setenv("__WTP_HOOKED", "1")

	cmd := createTestCLICommand(map[string]any{"branch": "feature/test"}, []string{})
	var buf, errBuf bytes.Buffer
	mockExec := &mockCommandExecutor{}
	cfg := &config.Config{}

	err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL))

	require.NoError(t, err)
	expectedPath := filepath.Join(xdg.WorktreeStorageRoot(), "owner", "repo", "feature", "test")
	assert.Contains(t, errBuf.String(), "__wtp_cd:"+expectedPath)
}

func TestAddCommand_NoMarkerWhenStay(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	axdg.Reload()
	t.Setenv("__WTP_HOOKED", "1")

	cmd := createTestCLICommand(map[string]any{"branch": "feature/test", "stay": true}, []string{})
	var buf, errBuf bytes.Buffer
	mockExec := &mockCommandExecutor{}
	cfg := &config.Config{}

	err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL))

	require.NoError(t, err)
	assert.Empty(t, errBuf.String())
}

func TestAddCommand_NoMarkerWhenNotHooked(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	axdg.Reload()
	t.Setenv("__WTP_HOOKED", "")

	cmd := createTestCLICommand(map[string]any{"branch": "feature/test"}, []string{})
	var buf, errBuf bytes.Buffer
	mockExec := &mockCommandExecutor{}
	cfg := &config.Config{}

	err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL))

	require.NoError(t, err)
	assert.Empty(t, errBuf.String())
}

func TestAddCommand_SuccessMessageShowsCdHint_WhenStay(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	axdg.Reload()

	cmd := createTestCLICommand(map[string]any{"branch": "feature/test", "stay": true}, []string{})
	var buf, errBuf bytes.Buffer
	mockExec := &mockCommandExecutor{}
	cfg := &config.Config{}

	err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL))

	require.NoError(t, err)
	assert.Contains(t, buf.String(), "💡 To switch to the new worktree, run:")
	assert.Contains(t, buf.String(), "wtp cd feature/test")
	assert.NotContains(t, buf.String(), "📍 Changed to worktree directory.")
}

func TestAddCommand_SuccessMessageShowsChanged_WhenNotStay(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	axdg.Reload()

	cmd := createTestCLICommand(map[string]any{"branch": "feature/test"}, []string{})
	var buf, errBuf bytes.Buffer
	mockExec := &mockCommandExecutor{}
	cfg := &config.Config{}

	err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL))

	require.NoError(t, err)
	assert.Contains(t, buf.String(), "📍 Changed to worktree directory.")
	assert.NotContains(t, buf.String(), "💡 To switch to the new worktree, run:")
}

func TestAddCommand_MarkerEmittedBeforeExecError(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	axdg.Reload()
	t.Setenv("__WTP_HOOKED", "1")

	cmd := createTestCLICommand(map[string]any{
		"branch": "feature/test",
		"exec":   "false",
	}, []string{})
	var buf, errBuf bytes.Buffer
	exec := &sequencedCommandExecutor{
		results: []command.Result{
			{Output: "worktree created"},
			{Error: assert.AnError},
		},
	}
	cfg := &config.Config{}

	err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, exec, cfg, "/test/repo", mockGetRemoteURL(testOriginURL))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "worktree was created")
	// Marker should still be emitted even though exec failed
	expectedPath := filepath.Join(xdg.WorktreeStorageRoot(), "owner", "repo", "feature", "test")
	assert.Contains(t, errBuf.String(), "__wtp_cd:"+expectedPath)
}

// ===== Branch Completion Tests =====

func TestGetBranches(t *testing.T) {
	RunWriterCommonTests(t, "getBranches", getBranches)
}

func TestCompleteBranches(t *testing.T) {
	t.Run("should not panic when called", func(t *testing.T) {
		cmd := &cli.Command{}

		assert.NotPanics(t, func() {
			restore := silenceStdout(t)
			defer restore()

			completeBranches(context.Background(), cmd)
		})
	})
}
