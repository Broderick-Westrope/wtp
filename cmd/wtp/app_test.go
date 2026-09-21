package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppShellCommandsSkipMaintenance(t *testing.T) {
	originalGetwd := maintGetwd
	originalRunCompletion := runCompletionCommand
	t.Cleanup(func() {
		maintGetwd = originalGetwd
		runCompletionCommand = originalRunCompletion
	})

	dir := t.TempDir()
	maintenanceCalls := 0
	maintGetwd = func() (string, error) {
		maintenanceCalls++
		return dir, nil
	}
	runCompletionCommand = func(shell string) ([]byte, error) {
		return []byte("completion-" + shell), nil
	}

	for _, command := range []string{"shell-init", "hook", "completion"} {
		for _, shell := range []string{"bash", "zsh", "fish"} {
			t.Run(command+"/"+shell, func(t *testing.T) {
				maintenanceCalls = 0
				var output bytes.Buffer
				app := newApp()
				app.Writer = &output
				app.ErrWriter = &output

				if command == "completion" {
					assert.NotEmpty(t, generateCompletionScript(t, shell))
				} else {
					require.NoError(t, app.Run(t.Context(), []string{"wtp", command, shell}))
					assert.NotEmpty(t, output.String())
				}
				assert.Zero(t, maintenanceCalls, "shell setup must not run repository maintenance")
			})
		}
	}

	for _, args := range [][]string{
		{"--help"},
		{"--version"},
		{"shell-init", "--help"},
		{"--generate-shell-completion"},
		{"cd", "--generate-shell-completion"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			maintenanceCalls = 0
			var output bytes.Buffer
			app := newApp()
			app.Writer = &output
			app.ErrWriter = &output

			require.NoError(t, app.Run(t.Context(), append([]string{"wtp"}, args...)))
			assert.Zero(t, maintenanceCalls)
		})
	}

	t.Run("worktree commands retain maintenance", func(t *testing.T) {
		maintenanceCalls = 0
		t.Chdir(dir)
		app := newApp()

		require.Error(t, app.Run(t.Context(), []string{"wtp", "cd"}))
		assert.Equal(t, 1, maintenanceCalls)
	})
}
