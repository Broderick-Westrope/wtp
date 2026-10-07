package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/cache"
	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
)

func defaultListDisplayOptionsForTests() listDisplayOptions {
	return listDisplayOptions{
		MaxPathWidth: defaultMaxPathWidth,
		OutputIsTTY:  true,
	}
}

// extractBranchColumnWidth measures the BRANCH column width from compact list output.
// The BRANCH column is first; its width is determined by where the two-space separator
// before HEAD (last column) occurs. Only works for borderless compact no-gh format
// (tests that use this helper always set listIsGHAvailable = false and enable compact).
func extractBranchColumnWidth(t *testing.T, output string) int {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		t.Fatalf("no output produced")
	}
	header := lines[0]
	if !strings.HasPrefix(header, "BRANCH") {
		t.Fatalf("BRANCH not first column in header: %q", header)
	}
	// Find HEAD which is always the last column
	headIdx := strings.LastIndex(header, "HEAD")
	if headIdx == -1 {
		t.Fatalf("HEAD column missing in header: %q", header)
	}
	// Branch column width = headIdx - 2 (two-space separator before HEAD)
	// when no gh columns in between, HEAD follows directly after BRANCH.
	// For no-gh: "%-bw*s  HEAD" → headIdx - 2 = bw
	return headIdx - 2
}

// ===== Command Structure Tests =====

func TestNewListCommand(t *testing.T) {
	cmd := newListCommand()

	assert.NotNil(t, cmd)
	assert.Equal(t, "list", cmd.Name)
	assert.Contains(t, cmd.Aliases, "ls")
	assert.Equal(t, "List all worktrees", cmd.Usage)
	assert.NotEmpty(t, cmd.Description)
	assert.NotNil(t, cmd.Action)
	assert.NotNil(t, cmd.ShellComplete)

	// Check new flags exist
	flagNames := make(map[string]bool)
	for _, flag := range cmd.Flags {
		for _, name := range flag.Names() {
			flagNames[name] = true
		}
	}
	assert.True(t, flagNames["all"], "should have --all flag")
	assert.True(t, flagNames["no-sync"], "should have --no-sync flag")
	assert.True(t, flagNames["quiet"], "should have --quiet flag")
	assert.True(t, flagNames["compact"], "should have --compact flag")
}

// ===== Pure Business Logic Tests =====

func TestDisplayConstants(t *testing.T) {
	assert.Equal(t, 8, headDisplayLength)
}

func TestWorktreeFormatting(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		branch         string
		head           string
		expectedFormat string
	}{
		{
			name:           "basic worktree",
			path:           "/path/to/worktree",
			branch:         "main",
			head:           "abcd1234",
			expectedFormat: "/path/to/worktree",
		},
		{
			name:           "long path",
			path:           "/very/long/path/to/worktree/that/might/need/truncation",
			branch:         "feature/test",
			head:           "efgh5678",
			expectedFormat: "/very/long/path/to/worktree/that/might/need/truncation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotEmpty(t, tt.path)
			assert.NotEmpty(t, tt.branch)
			assert.NotEmpty(t, tt.head)
		})
	}
}

// ===== Command Execution Tests =====

func TestListCommand_CommandConstruction(t *testing.T) {
	tests := []struct {
		name             string
		mockOutput       string
		expectedCommands []command.Command
	}{
		{
			name:       "list worktrees command",
			mockOutput: "worktree /path/to/worktree\nHEAD abc123\nbranch refs/heads/main\n\n",
			expectedCommands: []command.Command{{
				Name: "git",
				Args: []string{"worktree", "list", "--porcelain"},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldIsGH := listIsGHAvailable
			listIsGHAvailable = func() bool { return false }
			t.Cleanup(func() { listIsGHAvailable = oldIsGH })

			mockExec := &mockListCommandExecutor{
				results: []command.Result{
					{
						Output: tt.mockOutput,
						Error:  nil,
					},
				},
			}

			var buf bytes.Buffer
			cmd := &cli.Command{}

			err := listCommandWithCommandExecutor(
				context.Background(),
				cmd,
				&buf,
				mockExec,
				"/test/repo",
				defaultListDisplayOptionsForTests(),
			)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectedCommands, mockExec.executedCommands)
		})
	}
}

func TestListCommand_Output(t *testing.T) {
	tests := []struct {
		name           string
		mockOutput     string
		expectedOutput []string
	}{
		{
			name:       "single worktree",
			mockOutput: "worktree /path/to/worktree\nHEAD abc123\nbranch refs/heads/main\n\n",
			expectedOutput: []string{
				"BRANCH",
				"HEAD",
				"@", // Main worktree always shows as @
				"abc123",
			},
		},
		{
			name: "multiple worktrees",
			mockOutput: "worktree /path/to/main\nHEAD abc123\nbranch refs/heads/main\n\n" +
				"worktree /path/to/feature\nHEAD def456\nbranch refs/heads/feature/test\n\n",
			expectedOutput: []string{
				"BRANCH",
				"HEAD",
				"@",
				"feature/test", // Branch name in BRANCH column (main shows as @, not "main")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldIsGH := listIsGHAvailable
			listIsGHAvailable = func() bool { return false }
			t.Cleanup(func() { listIsGHAvailable = oldIsGH })

			oldGetwd := listGetwd
			listGetwd = func(context.Context) (string, error) {
				return "/path/to", nil
			}
			t.Cleanup(func() { listGetwd = oldGetwd })

			mockExec := &mockListCommandExecutor{
				results: []command.Result{
					{
						Output: tt.mockOutput,
						Error:  nil,
					},
				},
			}

			var buf bytes.Buffer
			cmd := &cli.Command{}

			err := listCommandWithCommandExecutor(
				context.Background(),
				cmd,
				&buf,
				mockExec,
				"/test/repo",
				defaultListDisplayOptionsForTests(),
			)

			assert.NoError(t, err)
			output := buf.String()
			for _, expected := range tt.expectedOutput {
				assert.Contains(t, output, expected)
			}
			// Should NOT contain PATH column
			assert.NotContains(t, output, "PATH")
		})
	}
}

// ===== Error Handling Tests =====

func TestListCommand_NotInGitRepo(t *testing.T) {
	tempDir := t.TempDir()
	oldDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldDir) }()
	err := os.Chdir(tempDir)
	assert.NoError(t, err)

	app := &cli.Command{
		Commands: []*cli.Command{
			newListCommand(),
		},
	}

	ctx := context.Background()
	err = app.Run(ctx, []string{"wtp", "list"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not in a git repository")
}

func TestListCommand_ExecutionError(t *testing.T) {
	mockExec := &mockListCommandExecutor{
		shouldFail: true,
		errorMsg:   "git command failed",
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		defaultListDisplayOptionsForTests(),
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "git command failed")
}

func TestListCommand_NoWorktrees(t *testing.T) {
	mockExec := &mockListCommandExecutor{
		results: []command.Result{
			{
				Output: "",
				Error:  nil,
			},
		},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		defaultListDisplayOptionsForTests(),
	)

	assert.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, "No worktrees found")
}

// ===== Edge Cases Tests =====

func TestListCommand_InternationalCharacters(t *testing.T) {
	tests := []struct {
		name         string
		branchName   string
		worktreePath string
	}{
		{
			name:         "Japanese characters",
			branchName:   "機能/ログイン",
			worktreePath: "/path/to/feature/japanese",
		},
		{
			name:         "Spanish accents",
			branchName:   "función/añadir",
			worktreePath: "/path/to/feature/spanish",
		},
		{
			name:         "Emoji characters",
			branchName:   "feature/🚀-rocket",
			worktreePath: "/path/to/feature/emoji",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldIsGH := listIsGHAvailable
			listIsGHAvailable = func() bool { return false }
			t.Cleanup(func() { listIsGHAvailable = oldIsGH })

			oldGetwd := listGetwd
			listGetwd = func(context.Context) (string, error) {
				return "/tmp", nil
			}
			t.Cleanup(func() { listGetwd = oldGetwd })

			// Include main worktree + the unicode branch worktree
			mockOutput := "worktree /path/to/main\nHEAD abc000\nbranch refs/heads/main\n\n" +
				"worktree " + tt.worktreePath + "\nHEAD abc123\nbranch refs/heads/" + tt.branchName + "\n\n"

			mockExec := &mockListCommandExecutor{
				results: []command.Result{
					{
						Output: mockOutput,
						Error:  nil,
					},
				},
			}

			var buf bytes.Buffer
			cmd := &cli.Command{}

			err := listCommandWithCommandExecutor(
				context.Background(),
				cmd,
				&buf,
				mockExec,
				"/test/repo",
				defaultListDisplayOptionsForTests(),
			)

			assert.NoError(t, err)
			output := buf.String()
			assert.Contains(t, output, tt.branchName)
			assert.Contains(t, output, "@")
		})
	}
}

func TestListCommand_LongBranchNames(t *testing.T) {
	tests := []struct {
		name       string
		branchName string
	}{
		{
			name:       "very long branch name",
			branchName: "feature/very-long-branch-name-that-might-cause-display-issues-in-terminal",
		},
		{
			name:       "branch with slashes",
			branchName: "feature/auth/oauth/google/callback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldIsGH := listIsGHAvailable
			listIsGHAvailable = func() bool { return false }
			t.Cleanup(func() { listIsGHAvailable = oldIsGH })

			oldGetwd := listGetwd
			listGetwd = func(context.Context) (string, error) {
				return "/tmp", nil
			}
			t.Cleanup(func() { listGetwd = oldGetwd })

			mockOutput := "worktree /tmp/worktree\nHEAD abc123\nbranch refs/heads/" + tt.branchName + "\n\n"

			mockExec := &mockListCommandExecutor{
				results: []command.Result{
					{
						Output: mockOutput,
						Error:  nil,
					},
				},
			}

			var buf bytes.Buffer
			cmd := &cli.Command{}

			err := listCommandWithCommandExecutor(
				context.Background(),
				cmd,
				&buf,
				mockExec,
				"/test/repo",
				defaultListDisplayOptionsForTests(),
			)

			assert.NoError(t, err)
			output := buf.String()
			// Main worktree should show as @
			assert.Contains(t, output, "@")
		})
	}
}

func TestListCommand_MixedWorktreeStates(t *testing.T) {
	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return false }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	oldGetwd := listGetwd
	listGetwd = func(context.Context) (string, error) {
		return "/path/to", nil
	}
	t.Cleanup(func() { listGetwd = oldGetwd })

	mockOutput := `worktree /path/to/main
HEAD abc123
branch refs/heads/main

worktree /path/to/detached
HEAD def456
detached

worktree /path/to/feature
HEAD ghi789
branch refs/heads/feature/test

`

	mockExec := &mockListCommandExecutor{
		results: []command.Result{
			{
				Output: mockOutput,
				Error:  nil,
			},
		},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		defaultListDisplayOptionsForTests(),
	)

	assert.NoError(t, err)
	output := buf.String()

	assert.Contains(t, output, "@")
	assert.Contains(t, output, "feature")
	assert.Contains(t, output, "feature/test")
	// Should show "(detached)" for detached HEAD (not "(detached HEAD)")
	assert.Contains(t, output, "(detached)")
	assert.NotContains(t, output, "(detached HEAD)")
	// main worktree shows as @ not "main"
	assert.NotContains(t, output, " main")
}

func TestListCommand_HeaderFormatting(t *testing.T) {
	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return false }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	mockExec := &mockListCommandExecutor{
		results: []command.Result{
			{
				Output: "worktree /path/to/worktree\nHEAD abc123\nbranch refs/heads/main\n\n",
				Error:  nil,
			},
		},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		defaultListDisplayOptionsForTests(),
	)

	assert.NoError(t, err)
	output := buf.String()

	lines := strings.Split(output, "\n")
	assert.True(t, len(lines) >= 4, "Should have border, header, and row lines")

	// New format: BRANCH and HEAD columns (no PATH, no STATUS)
	assert.Contains(t, output, "BRANCH")
	assert.Contains(t, output, "HEAD")
	assert.NotContains(t, output, "PATH")
	assert.NotContains(t, output, "STATUS")

	// Should render table borders (TTY output)
	assert.Contains(t, output, "│")
	assert.Contains(t, output, "─")
}

// ===== Mock Implementations =====

type mockListCommandExecutor struct {
	executedCommands []command.Command
	results          []command.Result
	shouldFail       bool
	errorMsg         string
}

func (m *mockListCommandExecutor) Execute(commands []command.Command) (*command.ExecutionResult, error) {
	m.executedCommands = commands

	if m.shouldFail {
		return nil, &mockError{message: m.errorMsg}
	}

	results := make([]command.Result, len(commands))
	for i, cmd := range commands {
		if i < len(m.results) {
			results[i] = m.results[i]
		} else {
			results[i] = command.Result{
				Command: cmd,
				Output:  "",
				Error:   nil,
			}
		}
	}

	return &command.ExecutionResult{Results: results}, nil
}

func TestListCommand_DetachedHeadFormatting(t *testing.T) {
	tests := []struct {
		name           string
		mockOutput     string
		expectedBranch string
		description    string
	}{
		{
			name: "empty branch should show (no branch)",
			mockOutput: `worktree /path/to/main
HEAD abc000
branch refs/heads/main

worktree /path/to/empty
HEAD abc123

`,
			expectedBranch: "(no branch)",
			description:    "Empty branch field should display as (no branch)",
		},
		{
			name: "detached keyword should show (detached)",
			mockOutput: `worktree /path/to/main
HEAD abc000
branch refs/heads/main

worktree /path/to/detached-head
HEAD def456
detached

`,
			expectedBranch: "(detached)",
			description:    "Detached keyword should display as (detached)",
		},
		{
			name: "normal branch should show as is",
			mockOutput: `worktree /path/to/main
HEAD abc000
branch refs/heads/main

worktree /path/to/normal
HEAD ghi789
branch refs/heads/feature/awesome

`,
			expectedBranch: "feature/awesome",
			description:    "Normal branch should display as is",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldIsGH := listIsGHAvailable
			listIsGHAvailable = func() bool { return false }
			t.Cleanup(func() { listIsGHAvailable = oldIsGH })

			mockExec := &mockListCommandExecutor{
				results: []command.Result{
					{Output: tt.mockOutput, Error: nil},
				},
			}

			var buf bytes.Buffer
			cmd := &cli.Command{}

			err := listCommandWithCommandExecutor(
				context.Background(),
				cmd,
				&buf,
				mockExec,
				"/repo",
				defaultListDisplayOptionsForTests(),
			)

			assert.NoError(t, err, tt.description)
			output := buf.String()
			assert.Contains(t, output, tt.expectedBranch, tt.description)
		})
	}
}

type mockError struct {
	message string
}

func (e *mockError) Error() string {
	return e.message
}

func TestListCommand_BranchDisplay(t *testing.T) {
	tests := []struct {
		name             string
		mockOutput       string
		currentPath      string
		expectedContains []string
		description      string
	}{
		{
			name: "main worktree should display as @",
			mockOutput: `worktree /Users/satoshi/dev/project
HEAD abc123
branch refs/heads/main

worktree /Users/satoshi/dev/project/.worktrees/feature
HEAD def456
branch refs/heads/feature/test

`,
			currentPath: "/Users/satoshi/dev/project/.worktrees/feature",
			expectedContains: []string{
				"@",
				"feature/test", // Branch name, not path
				"*",            // Current worktree marker
			},
			description: "Main worktree should show as @ and current should have *",
		},
		{
			name: "branch names in BRANCH column",
			mockOutput: `worktree /Users/satoshi/dev/project
HEAD abc123
branch refs/heads/main

worktree /Users/satoshi/dev/project-feature
HEAD def456
branch refs/heads/feature

`,
			currentPath: "/Users/satoshi/dev",
			expectedContains: []string{
				"@",       // Main worktree
				"feature", // Branch name (not path)
			},
			description: "Should show branch names, not directory paths",
		},
		{
			name: "multiple non-main worktrees show branch names",
			mockOutput: `worktree /Users/satoshi/dev/src/github.com/satococoa/giselle
HEAD 043130cca
branch refs/heads/main

worktree /Users/satoshi/dev/src/github.com/satococoa/giselle/.worktrees/foobar
HEAD 043130cca
branch refs/heads/foobar

worktree /Users/satoshi/dev/src/github.com/satococoa/giselle/.worktrees/hoge
HEAD 043130cca
branch refs/heads/hoge

`,
			currentPath: "/Users/satoshi/dev/src/github.com/satococoa/giselle/.worktrees/foobar",
			expectedContains: []string{
				"@",
				"foobar*", // Current worktree with marker
				"hoge",
			},
			description: "Non-main worktrees should show branch names",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldIsGH := listIsGHAvailable
			listIsGHAvailable = func() bool { return false }
			t.Cleanup(func() { listIsGHAvailable = oldIsGH })

			oldGetwd := listGetwd
			listGetwd = func(context.Context) (string, error) {
				return tt.currentPath, nil
			}
			t.Cleanup(func() { listGetwd = oldGetwd })

			mockExec := &mockListCommandExecutor{
				results: []command.Result{
					{Output: tt.mockOutput, Error: nil},
				},
			}

			var buf bytes.Buffer
			cmd := &cli.Command{}

			err := listCommandWithCommandExecutor(
				context.Background(),
				cmd,
				&buf,
				mockExec,
				"/test/repo",
				defaultListDisplayOptionsForTests(),
			)

			assert.NoError(t, err, tt.description)
			output := buf.String()

			for _, expected := range tt.expectedContains {
				assert.Contains(t, output, expected, "Expected to find: %s in output: %s", expected, output)
			}
		})
	}
}

func TestListCommand_TerminalWidthTruncation(t *testing.T) {
	tests := []struct {
		name          string
		mockOutput    string
		terminalWidth int
		description   string
	}{
		{
			name: "output fits within terminal width",
			mockOutput: `worktree /Users/satoshi/dev/src/github.com/giselles-ai/giselle
HEAD 5d46cc7a
branch refs/heads/add-github-pull-request-ingestion-table

worktree /Users/satoshi/dev/src/github.com/giselles-ai/giselle/.worktrees/stripe-basil-update
HEAD 7c81ef4f
branch refs/heads/stripe-basil-migration

`,
			terminalWidth: 80,
			description:   "Output lines should not exceed terminal width",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldIsGH := listIsGHAvailable
			listIsGHAvailable = func() bool { return false }
			t.Cleanup(func() { listIsGHAvailable = oldIsGH })

			oldGetTerminalWidth := getTerminalWidth
			getTerminalWidth = func(context.Context) int {
				return tt.terminalWidth
			}
			t.Cleanup(func() { getTerminalWidth = oldGetTerminalWidth })

			mockExec := &mockListCommandExecutor{
				results: []command.Result{
					{Output: tt.mockOutput, Error: nil},
				},
			}

			var buf bytes.Buffer
			cmd := &cli.Command{}

			err := listCommandWithCommandExecutor(
				context.Background(),
				cmd,
				&buf,
				mockExec,
				"/repo",
				defaultListDisplayOptionsForTests(),
			)

			assert.NoError(t, err, tt.description)
			output := buf.String()

			assert.Contains(t, output, "BRANCH")
			assert.Contains(t, output, "HEAD")

			// Check that output fits within terminal width (display width, not bytes)
			lines := strings.Split(strings.TrimSpace(output), "\n")
			for _, line := range lines {
				assert.LessOrEqual(t, lipgloss.Width(line), tt.terminalWidth,
					"Line should not exceed terminal width: %s", line)
			}
		})
	}
}

func TestListCommand_BranchColumnCappedByMaxWidth(t *testing.T) {
	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return false }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/feature/very-long-branch-name-that-exceeds-max-width
HEAD def456
branch refs/heads/feature/very-long-branch-name-that-exceeds-max-width

`

	oldGetTerminalWidth := getTerminalWidth
	getTerminalWidth = func(context.Context) int { return 150 }
	t.Cleanup(func() { getTerminalWidth = oldGetTerminalWidth })

	mockExec := &mockListCommandExecutor{
		results: []command.Result{{Output: mockOutput, Error: nil}},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	opts := defaultListDisplayOptionsForTests()
	opts.MaxPathWidth = 30

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		opts,
	)
	assert.NoError(t, err)
	output := buf.String()

	// Branch names longer than the max width are truncated with an ellipsis.
	assert.NotContains(t, output, "feature/very-long-branch-name-that-exceeds-max-width")
	assert.Contains(t, output, "...")
	expected := truncateStr("feature/very-long-branch-name-that-exceeds-max-width", 30)
	assert.Contains(t, output, expected)
}

func TestListCommand_WideTerminalKeepsBorderedTable(t *testing.T) {
	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return false }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/feature/test
HEAD def456
branch refs/heads/feature/test

`

	oldGetTerminalWidth := getTerminalWidth
	getTerminalWidth = func(context.Context) int { return 200 }
	t.Cleanup(func() { getTerminalWidth = oldGetTerminalWidth })

	mockExec := &mockListCommandExecutor{
		results: []command.Result{{Output: mockOutput, Error: nil}},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		defaultListDisplayOptionsForTests(),
	)
	assert.NoError(t, err)
	output := buf.String()

	// TTY output keeps the bordered table regardless of terminal width;
	// columns are always sized to content.
	assert.Contains(t, output, "│")
	assert.Contains(t, output, "feature/test")
}

// runCompactTest is a helper for compact mode tests that returns the branch column width.
func runCompactTest(t *testing.T, opts listDisplayOptions) int {
	t.Helper()

	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return false }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/feature/test
HEAD def456
branch refs/heads/feature/test

`

	oldGetTerminalWidth := getTerminalWidth
	getTerminalWidth = func(context.Context) int { return 120 }
	t.Cleanup(func() { getTerminalWidth = oldGetTerminalWidth })

	mockExec := &mockListCommandExecutor{
		results: []command.Result{{Output: mockOutput, Error: nil}},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd, &buf, mockExec, "/test/repo",
		opts,
	)
	assert.NoError(t, err)

	return extractBranchColumnWidth(t, buf.String())
}

func TestListCommand_AutoCompactForNonTTY(t *testing.T) {
	opts := defaultListDisplayOptionsForTests()
	opts.OutputIsTTY = false

	width := runCompactTest(t, opts)
	// Non-TTY triggers compact: branch width = max branch name = len("feature/test") = 12
	assert.Equal(t, len("feature/test"), width)
}

func TestListCommand_CompactFlag(t *testing.T) {
	opts := defaultListDisplayOptionsForTests()
	opts.Compact = true

	width := runCompactTest(t, opts)
	// Compact: branch width = max branch name = len("feature/test") = 12
	assert.Equal(t, len("feature/test"), width)
}

func TestListCommand_CompactHasNoBorders(t *testing.T) {
	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return false }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	mockExec := &mockListCommandExecutor{
		results: []command.Result{
			{Output: "worktree /test/repo\nHEAD abc123\nbranch refs/heads/main\n\n", Error: nil},
		},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	opts := defaultListDisplayOptionsForTests()
	opts.Compact = true
	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd, &buf, mockExec, "/test/repo",
		opts,
	)
	assert.NoError(t, err)
	output := buf.String()

	assert.Contains(t, output, "BRANCH")
	assert.NotContains(t, output, "│", "compact output should have no borders")
	assert.NotContains(t, output, "─", "compact output should have no borders")
}

// ===== Quiet Mode Tests =====

func TestListCommand_QuietMode_SingleWorktree(t *testing.T) {
	mockExec := &mockListCommandExecutor{
		results: []command.Result{
			{
				Output: "worktree /test/repo\nHEAD abc123\nbranch refs/heads/main\n\n",
				Error:  nil,
			},
		},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	opts := defaultListDisplayOptionsForTests()
	opts.Quiet = true
	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		opts,
	)

	assert.NoError(t, err)
	output := buf.String()

	// Should only contain the worktree name (@), nothing else
	assert.Equal(t, "@\n", output)
	assert.NotContains(t, output, "PATH")
	assert.NotContains(t, output, "BRANCH")
	assert.NotContains(t, output, "HEAD")
}

func TestListCommand_QuietMode_MultipleWorktrees(t *testing.T) {
	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/feature/test
HEAD def456
branch refs/heads/feature/test

worktree /test/repo/.worktrees/feature/another
HEAD ghi789
branch refs/heads/feature/another

`

	mockExec := &mockListCommandExecutor{
		results: []command.Result{
			{Output: mockOutput, Error: nil},
		},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	opts := defaultListDisplayOptionsForTests()
	opts.Quiet = true
	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		opts,
	)

	assert.NoError(t, err)
	output := buf.String()

	// Should contain all three worktree branch names, one per line
	expectedOutput := "@\nfeature/test\nfeature/another\n"
	assert.Equal(t, expectedOutput, output)

	assert.NotContains(t, output, "PATH")
	assert.NotContains(t, output, "BRANCH")
	assert.NotContains(t, output, "HEAD")
	assert.NotContains(t, output, "----")
}

func TestListCommand_QuietMode_NoWorktrees(t *testing.T) {
	mockExec := &mockListCommandExecutor{
		results: []command.Result{
			{
				Output: "",
				Error:  nil,
			},
		},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	opts := defaultListDisplayOptionsForTests()
	opts.Quiet = true
	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		opts,
	)

	assert.NoError(t, err)
	output := buf.String()
	assert.Equal(t, "", output)
	assert.NotContains(t, output, "No worktrees found")
}

func TestListCommand_QuietMode_DetachedHead(t *testing.T) {
	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/detached
HEAD def456
detached

`

	mockExec := &mockListCommandExecutor{
		results: []command.Result{
			{Output: mockOutput, Error: nil},
		},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	opts := defaultListDisplayOptionsForTests()
	opts.Quiet = true
	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		opts,
	)

	assert.NoError(t, err)
	output := buf.String()

	// Detached HEAD worktrees are OMITTED from quiet output
	expectedOutput := "@\n"
	assert.Equal(t, expectedOutput, output)
	assert.NotContains(t, output, "detached")
	assert.NotContains(t, output, "BRANCH")
	assert.NotContains(t, output, "HEAD")
}

func TestCompleteList_SuggestsQuietFlag(t *testing.T) {
	t.Run("suggests quiet flag alias", func(t *testing.T) {
		originalArgs := os.Args
		t.Cleanup(func() { os.Args = originalArgs })

		os.Args = []string{"wtp", "list", "--q", "--generate-shell-completion"}

		var buf bytes.Buffer
		cmd := newListCommand()
		cmd.Writer = &buf

		cmd.ShellComplete(context.Background(), cmd)

		assert.Contains(t, buf.String(), "--quiet")
	})
}

// ===== New Phase 3 Tests =====

func TestListCommand_NoGH_NoPRCIColumns(t *testing.T) {
	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return false }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/feature/auth
HEAD def456
branch refs/heads/feature/auth

`

	mockExec := &mockListCommandExecutor{
		results: []command.Result{
			{Output: mockOutput, Error: nil},
		},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd, &buf, mockExec, "/test/repo",
		defaultListDisplayOptionsForTests(),
	)

	assert.NoError(t, err)
	output := buf.String()

	// No PR/CI columns when gh not available
	assert.NotContains(t, output, " PR ")
	assert.NotContains(t, output, " CI ")
	// Branch and HEAD columns present
	assert.Contains(t, output, "BRANCH")
	assert.Contains(t, output, "HEAD")
}

func TestListCommand_AllShowsArchived(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	axdg.Reload()

	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return false }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	oldGetRemote := listGetRemoteURL
	listGetRemoteURL = func(_ context.Context, _ string) (string, error) {
		return "https://github.com/owner/repo.git", nil
	}
	t.Cleanup(func() { listGetRemoteURL = oldGetRemote })

	// Pre-archive feature/auth
	repoID := remote.RepoIdentifier{Owner: "owner", Repo: "repo"}
	stateStore := state.NewStore()
	_ = stateStore.SetArchived(repoID.StateKey("feature/auth"), true)

	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/feature/auth
HEAD def456
branch refs/heads/feature/auth

worktree /test/repo/.worktrees/feature/other
HEAD ghi789
branch refs/heads/feature/other

`

	mockExec := &mockListCommandExecutor{
		results: []command.Result{
			{Output: mockOutput, Error: nil},
		},
	}

	t.Run("without --all, archived hidden", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := &cli.Command{}

		err := listCommandWithCommandExecutor(
			context.Background(),
			cmd, &buf, mockExec, "/test/repo",
			defaultListDisplayOptionsForTests(),
		)
		assert.NoError(t, err)
		output := buf.String()
		assert.NotContains(t, output, "feature/auth", "archived branch should be hidden")
		assert.Contains(t, output, "feature/other")
	})

	t.Run("with --all, archived shown", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := &cli.Command{}

		opts := defaultListDisplayOptionsForTests()
		opts.ShowAll = true
		err := listCommandWithCommandExecutor(
			context.Background(),
			cmd, &buf, mockExec, "/test/repo",
			opts,
		)
		assert.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "feature/auth", "archived branch should appear with --all")
		assert.Contains(t, output, "(archived)", "should show (archived) marker")
	})
}

// TestListCommand_AllSynthesizesArchivedFromState verifies that --all shows
// archived entries that exist only in state.json (destructive archive removed
// them from git worktree list entirely).
func TestListCommand_AllSynthesizesArchivedFromState(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	axdg.Reload()

	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return false }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	oldGetRemote := listGetRemoteURL
	listGetRemoteURL = func(_ context.Context, _ string) (string, error) {
		return "https://github.com/owner/repo.git", nil
	}
	t.Cleanup(func() { listGetRemoteURL = oldGetRemote })

	// Destructively-archived entry: exists in state with full metadata,
	// but not in git worktree list.
	repoID := remote.RepoIdentifier{Owner: "owner", Repo: "repo"}
	stateStore := state.NewStore()
	require.NoError(t, stateStore.SetArchivedFull(repoID.StateKey("feature/gone"), &state.WorktreeState{
		Archived:     true,
		ArchivedAt:   time.Now(),
		CommitSHA:    "cafebabe12345678",
		Branch:       "feature/gone",
		WorktreePath: "/test/repo/.worktrees/feature/gone",
	}))

	// Entry for a different repo must not leak into this repo's listing.
	otherRepoID := remote.RepoIdentifier{Owner: "other", Repo: "repo"}
	require.NoError(t, stateStore.SetArchivedFull(otherRepoID.StateKey("feature/foreign"), &state.WorktreeState{
		Archived:  true,
		CommitSHA: "deadbeef12345678",
		Branch:    "feature/foreign",
	}))

	// Git only knows about main — feature/gone was destructively archived.
	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

`

	t.Run("with --all, synthesized from state", func(t *testing.T) {
		mockExec := &mockListCommandExecutor{
			results: []command.Result{{Output: mockOutput, Error: nil}},
		}
		var buf bytes.Buffer
		cmd := &cli.Command{}

		opts := defaultListDisplayOptionsForTests()
		opts.ShowAll = true
		err := listCommandWithCommandExecutor(
			context.Background(), cmd, &buf, mockExec, "/test/repo", opts,
		)
		assert.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "feature/gone", "destructively archived branch should appear with --all")
		assert.Contains(t, output, "(archived)", "should show (archived) marker")
		assert.Contains(t, output, "cafebabe", "should show recorded SHA as HEAD")
		assert.NotContains(t, output, "feature/foreign", "other repo's entries must not appear")
	})

	t.Run("without --all, absent", func(t *testing.T) {
		mockExec := &mockListCommandExecutor{
			results: []command.Result{{Output: mockOutput, Error: nil}},
		}
		var buf bytes.Buffer
		cmd := &cli.Command{}

		err := listCommandWithCommandExecutor(
			context.Background(), cmd, &buf, mockExec, "/test/repo",
			defaultListDisplayOptionsForTests(),
		)
		assert.NoError(t, err)
		assert.NotContains(t, buf.String(), "feature/gone")
	})

	t.Run("quiet with --all includes bare names", func(t *testing.T) {
		mockExec := &mockListCommandExecutor{
			results: []command.Result{{Output: mockOutput, Error: nil}},
		}
		var buf bytes.Buffer
		cmd := &cli.Command{}

		opts := defaultListDisplayOptionsForTests()
		opts.ShowAll = true
		opts.Quiet = true
		err := listCommandWithCommandExecutor(
			context.Background(), cmd, &buf, mockExec, "/test/repo", opts,
		)
		assert.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "feature/gone\n", "quiet --all should include archived branch as bare name")
		assert.NotContains(t, output, "(archived)", "quiet output must not include the archived label")
	})
}

const listTestWorktrees = `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/feature/auth
HEAD def456
branch refs/heads/feature/auth

`

// setupListGHTest isolates state and cache, pretends gh is installed and the
// repo has a GitHub origin, and counts background refreshes the list starts.
func setupListGHTest(t *testing.T) *int {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	axdg.Reload()
	t.Cleanup(axdg.Reload)

	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return true }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	oldGetRemote := listGetRemoteURL
	listGetRemoteURL = func(_ context.Context, _ string) (string, error) {
		return "https://github.com/owner/repo.git", nil
	}
	t.Cleanup(func() { listGetRemoteURL = oldGetRemote })

	spawns := 0
	oldSpawn := listSpawnBackgroundSync
	listSpawnBackgroundSync = func(context.Context) { spawns++ }
	t.Cleanup(func() { listSpawnBackgroundSync = oldSpawn })
	return &spawns
}

func runListForTest(t *testing.T, opts listDisplayOptions) string {
	t.Helper()
	mockExec := &mockListCommandExecutor{
		results: []command.Result{{Output: listTestWorktrees}},
	}
	var buf bytes.Buffer
	err := listCommandWithCommandExecutor(context.Background(), &cli.Command{}, &buf, mockExec, "/test/repo", opts)
	require.NoError(t, err)
	return buf.String()
}

func seedListCache(t *testing.T, entry *cache.WorktreeCache) {
	t.Helper()
	repoID := remote.RepoIdentifier{Owner: "owner", Repo: "repo"}
	require.NoError(t, cache.NewStore().SetBatch(map[string]cache.WorktreeCache{
		repoID.StateKey("feature/auth"): *entry,
	}))
}

func TestListCommand_ShowsCachedPRCIWithoutRefreshWhenFresh(t *testing.T) {
	spawns := setupListGHTest(t)
	seedListCache(t, &cache.WorktreeCache{PRNumber: 42, PRState: "OPEN", CIStatus: "✓ CI passing"})

	output := runListForTest(t, defaultListDisplayOptionsForTests())

	assert.Contains(t, output, " PR ")
	assert.Contains(t, output, " CI ")
	assert.Contains(t, output, "#42 Ready")
	assert.Contains(t, output, "✓ CI passing")
	assert.Zero(t, *spawns, "fresh cache needs no refresh")
}

func TestListCommand_StaleCacheStartsBackgroundRefresh(t *testing.T) {
	spawns := setupListGHTest(t)
	seedListCache(t, &cache.WorktreeCache{
		PRNumber: 42, PRState: "OPEN", CIStatus: "● 1 pending",
		UpdatedAt: time.Now().Add(-time.Hour),
	})

	output := runListForTest(t, defaultListDisplayOptionsForTests())

	assert.Contains(t, output, "#42 Ready", "stale data is still shown rather than waited on")
	assert.Equal(t, 1, *spawns)
}

func TestListCommand_UncachedBranchStartsBackgroundRefresh(t *testing.T) {
	spawns := setupListGHTest(t)

	output := runListForTest(t, defaultListDisplayOptionsForTests())

	assert.Contains(t, output, "feature/auth")
	assert.Equal(t, 1, *spawns)
}

func TestListCommand_RefreshAttemptsAreSpacedByTTL(t *testing.T) {
	spawns := setupListGHTest(t)

	runListForTest(t, defaultListDisplayOptionsForTests())
	runListForTest(t, defaultListDisplayOptionsForTests())

	assert.Equal(t, 1, *spawns, "a refresh that cached nothing (gh offline) must not be retried on every list")
}

func TestListCommand_NoSyncSkipsBackgroundRefresh(t *testing.T) {
	spawns := setupListGHTest(t)

	opts := defaultListDisplayOptionsForTests()
	opts.NoSync = true
	runListForTest(t, opts)

	assert.Zero(t, *spawns)
}

func TestListCommand_EnvDisablesBackgroundRefresh(t *testing.T) {
	spawns := setupListGHTest(t)

	for _, value := range []string{"1", "true"} {
		env := &procenv.Env{Dir: "/test/repo", Environ: []string{noBackgroundSyncEnv + "=" + value}}
		ctx := procenv.WithEnv(context.Background(), env)
		mockExec := &mockListCommandExecutor{results: []command.Result{{Output: listTestWorktrees}}}
		var buf bytes.Buffer
		require.NoError(t, listCommandWithCommandExecutor(
			ctx, &cli.Command{}, &buf, mockExec, "/test/repo", defaultListDisplayOptionsForTests(),
		))
		assert.Contains(t, buf.String(), "feature/auth")
	}
	assert.Zero(t, *spawns)
}

func TestListCommand_QuietSkipsPRCI(t *testing.T) {
	spawns := setupListGHTest(t)

	opts := defaultListDisplayOptionsForTests()
	opts.Quiet = true
	output := runListForTest(t, opts)

	assert.Equal(t, "@\nfeature/auth\n", output)
	assert.Zero(t, *spawns)
}

// TestListCommand_DoesNotAutoArchive verifies that wtp list never archives,
// even when the cache says a PR merged — that is `wtp sync`'s job.
func TestListCommand_DoesNotAutoArchive(t *testing.T) {
	setupListGHTest(t)
	seedListCache(t, &cache.WorktreeCache{PRNumber: 42, PRState: "MERGED", CIStatus: "-"})

	output := runListForTest(t, defaultListDisplayOptionsForTests())

	assert.Contains(t, output, "feature/auth")
	assert.Contains(t, output, "#42 Merged")
	repoID := remote.RepoIdentifier{Owner: "owner", Repo: "repo"}
	assert.False(t, state.NewStore().IsArchived(repoID.StateKey("feature/auth")))
}

func TestListCommand_DetachedHeadWithMarker(t *testing.T) {
	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return false }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/detached
HEAD def456
detached

`

	mockExec := &mockListCommandExecutor{
		results: []command.Result{
			{Output: mockOutput, Error: nil},
		},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd, &buf, mockExec, "/test/repo",
		defaultListDisplayOptionsForTests(),
	)

	assert.NoError(t, err)
	output := buf.String()

	// Detached HEAD shows with (detached) marker, not (detached HEAD)
	assert.Contains(t, output, "(detached)")
	assert.NotContains(t, output, "(detached HEAD)")
}
