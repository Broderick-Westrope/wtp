package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"

	"github.com/satococoa/wtp/v3/internal/command"
	"github.com/satococoa/wtp/v3/internal/github"
	"github.com/satococoa/wtp/v3/internal/remote"
	"github.com/satococoa/wtp/v3/internal/state"
)

func defaultListDisplayOptionsForTests() listDisplayOptions {
	return listDisplayOptions{
		MaxPathWidth: defaultMaxPathWidth,
		OutputIsTTY:  true,
	}
}

// extractBranchColumnWidth measures the BRANCH column width from list output.
// The BRANCH column is first; its width is determined by where the two-space separator
// before HEAD (last column) occurs. Works for both gh and no-gh formats.
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
	cmd := NewListCommand()

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
	assert.Equal(t, 6, branchHeaderDashes)
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
				false, false, false,
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
			listGetwd = func() (string, error) {
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
				false, false, false,
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
			NewListCommand(),
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
		false, false, false,
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
		false, false, false,
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
			listGetwd = func() (string, error) {
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
				false, false, false,
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
			listGetwd = func() (string, error) {
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
				false, false, false,
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
	listGetwd = func() (string, error) {
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
		false, false, false,
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
		false, false, false,
		defaultListDisplayOptionsForTests(),
	)

	assert.NoError(t, err)
	output := buf.String()

	lines := strings.Split(output, "\n")
	assert.True(t, len(lines) >= 2, "Should have header and separator lines")

	// New format: BRANCH and HEAD columns (no PATH, no STATUS)
	assert.Contains(t, output, "BRANCH")
	assert.Contains(t, output, "HEAD")
	assert.NotContains(t, output, "PATH")
	assert.NotContains(t, output, "STATUS")

	// Should contain separator dashes
	assert.Contains(t, output, "----")
	assert.Contains(t, output, "------")
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
				false, false, false,
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
			listGetwd = func() (string, error) {
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
				false, false, false,
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
			getTerminalWidth = func() int {
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
				false, false, false,
				defaultListDisplayOptionsForTests(),
			)

			assert.NoError(t, err, tt.description)
			output := buf.String()

			assert.Contains(t, output, "BRANCH")
			assert.Contains(t, output, "HEAD")

			// Check that output fits within terminal width
			lines := strings.Split(strings.TrimSpace(output), "\n")
			for _, line := range lines {
				assert.LessOrEqual(t, len(line), tt.terminalWidth,
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
	getTerminalWidth = func() int { return 150 }
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
		false, false, false,
		opts,
	)
	assert.NoError(t, err)

	width := extractBranchColumnWidth(t, buf.String())
	assert.Equal(t, 30, width)
}

func TestListCommand_AutoCompactForSuperWideTerminals(t *testing.T) {
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
	getTerminalWidth = func() int { return 200 }
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
		false, false, false,
		defaultListDisplayOptionsForTests(),
	)
	assert.NoError(t, err)

	width := extractBranchColumnWidth(t, buf.String())
	// Compact triggered at >= 160: branch width = max branch name length = len("feature/test") = 12
	assert.Equal(t, len("feature/test"), width)
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
	getTerminalWidth = func() int { return 120 }
	t.Cleanup(func() { getTerminalWidth = oldGetTerminalWidth })

	mockExec := &mockListCommandExecutor{
		results: []command.Result{{Output: mockOutput, Error: nil}},
	}

	var buf bytes.Buffer
	cmd := &cli.Command{}

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd, &buf, mockExec, "/test/repo",
		false, false, false, opts,
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

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		true, false, false,
		defaultListDisplayOptionsForTests(),
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

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		true, false, false,
		defaultListDisplayOptionsForTests(),
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

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		true, false, false,
		defaultListDisplayOptionsForTests(),
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

	err := listCommandWithCommandExecutor(
		context.Background(),
		cmd,
		&buf,
		mockExec,
		"/test/repo",
		true, false, false,
		defaultListDisplayOptionsForTests(),
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
		cmd := NewListCommand()
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
		false, false, false,
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
	listGetRemoteURL = func(_ string) (string, error) {
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
			false, false, false, // quiet=false, showAll=false, noSync=false
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

		err := listCommandWithCommandExecutor(
			context.Background(),
			cmd, &buf, mockExec, "/test/repo",
			false, true, false, // quiet=false, showAll=true, noSync=false
			defaultListDisplayOptionsForTests(),
		)
		assert.NoError(t, err)
		output := buf.String()
		assert.Contains(t, output, "feature/auth", "archived branch should appear with --all")
		assert.Contains(t, output, "(archived)", "should show (archived) marker")
	})
}

func TestListCommand_NoSync_SkipsGHCalls(t *testing.T) {
	ghCallCount := 0

	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return true }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	oldGetPR := listGetPRForBranch
	listGetPRForBranch = func(_ context.Context, _ string) (*github.PRInfo, error) {
		ghCallCount++
		return nil, nil
	}
	t.Cleanup(func() { listGetPRForBranch = oldGetPR })

	oldGetCI := listGetCIStatus
	listGetCIStatus = func(_ context.Context, _ string) (*github.CIStatus, error) {
		ghCallCount++
		return nil, nil
	}
	t.Cleanup(func() { listGetCIStatus = oldGetCI })

	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/feature/test
HEAD def456
branch refs/heads/feature/test

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
		false, false, true, // noSync=true
		defaultListDisplayOptionsForTests(),
	)

	assert.NoError(t, err)
	assert.Equal(t, 0, ghCallCount, "gh calls should be skipped with --no-sync")
}

func TestListCommand_AutoArchiveMergedPR(t *testing.T) {
	dataDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)

	t.Setenv("XDG_CACHE_HOME", cacheDir)
	axdg.Reload()

	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return true }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	oldGetRemote := listGetRemoteURL
	listGetRemoteURL = func(_ string) (string, error) {
		return "https://github.com/owner/repo.git", nil
	}
	t.Cleanup(func() { listGetRemoteURL = oldGetRemote })

	oldGetPR := listGetPRForBranch
	listGetPRForBranch = func(_ context.Context, branch string) (*github.PRInfo, error) {
		if branch == "feature/merged" {
			return &github.PRInfo{Number: 42, State: "MERGED", Title: "Merged PR"}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { listGetPRForBranch = oldGetPR })

	oldGetCI := listGetCIStatus
	listGetCIStatus = func(_ context.Context, _ string) (*github.CIStatus, error) {
		return nil, nil
	}
	t.Cleanup(func() { listGetCIStatus = oldGetCI })

	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/feature/merged
HEAD def456
branch refs/heads/feature/merged

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
		false, false, false,
		defaultListDisplayOptionsForTests(),
	)

	assert.NoError(t, err)
	output := buf.String()

	// Auto-archive notice should be printed
	assert.Contains(t, output, "Auto-archived feature/merged (PR #42 merged)")

	// The merged branch should NOT appear as a table row (it was auto-archived, showAll=false)
	// Split output: first line is auto-archive notice, rest is table
	lines := strings.Split(strings.TrimSpace(output), "\n")
	// Find the table section (after the auto-archive notice line)
	var tableLines []string
	for _, line := range lines {
		if !strings.HasPrefix(line, "Auto-archived") {
			tableLines = append(tableLines, line)
		}
	}
	tableOutput := strings.Join(tableLines, "\n")
	assert.NotContains(t, tableOutput, "feature/merged", "merged branch should not appear in table")

	// State should be updated
	repoID := remote.RepoIdentifier{Owner: "owner", Repo: "repo"}
	stateStore := state.NewStore()
	assert.True(t, stateStore.IsArchived(repoID.StateKey("feature/merged")))
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
		false, false, false,
		defaultListDisplayOptionsForTests(),
	)

	assert.NoError(t, err)
	output := buf.String()

	// Detached HEAD shows with (detached) marker, not (detached HEAD)
	assert.Contains(t, output, "(detached)")
	assert.NotContains(t, output, "(detached HEAD)")
}

func TestListCommand_QuietNoSyncSideEffectFree(t *testing.T) {
	ghCallCount := 0

	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return true }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	oldGetPR := listGetPRForBranch
	listGetPRForBranch = func(_ context.Context, _ string) (*github.PRInfo, error) {
		ghCallCount++
		return nil, nil
	}
	t.Cleanup(func() { listGetPRForBranch = oldGetPR })

	mockOutput := `worktree /test/repo
HEAD abc123
branch refs/heads/main

worktree /test/repo/.worktrees/feature/test
HEAD def456
branch refs/heads/feature/test

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
		true, false, true, // quiet=true, noSync=true
		defaultListDisplayOptionsForTests(),
	)

	assert.NoError(t, err)
	assert.Equal(t, 0, ghCallCount, "quiet+no-sync should make no gh calls")
	output := buf.String()
	// Quiet mode outputs branch names
	assert.Equal(t, "@\nfeature/test\n", output)
}

func TestListCommand_WithGHColumns(t *testing.T) {
	dataDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)

	t.Setenv("XDG_CACHE_HOME", cacheDir)
	axdg.Reload()

	oldIsGH := listIsGHAvailable
	listIsGHAvailable = func() bool { return true }
	t.Cleanup(func() { listIsGHAvailable = oldIsGH })

	oldGetRemote := listGetRemoteURL
	listGetRemoteURL = func(_ string) (string, error) {
		return "https://github.com/owner/repo.git", nil
	}
	t.Cleanup(func() { listGetRemoteURL = oldGetRemote })

	oldGetPR := listGetPRForBranch
	listGetPRForBranch = func(_ context.Context, branch string) (*github.PRInfo, error) {
		if branch == "feature/auth" {
			return &github.PRInfo{Number: 42, State: "OPEN"}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { listGetPRForBranch = oldGetPR })

	oldGetCI := listGetCIStatus
	listGetCIStatus = func(_ context.Context, branch string) (*github.CIStatus, error) {
		if branch == "feature/auth" {
			return &github.CIStatus{State: "passing", Total: 3, Passing: 3}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { listGetCIStatus = oldGetCI })

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
		false, false, false,
		defaultListDisplayOptionsForTests(),
	)

	assert.NoError(t, err)
	output := buf.String()

	// With gh: PR and CI columns present
	assert.Contains(t, output, " PR ")
	assert.Contains(t, output, " CI ")
	assert.Contains(t, output, "#42")
}
