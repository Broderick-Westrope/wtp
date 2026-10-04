package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEmbeddedTestRepo(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Test User"},
		{"config", "user.email", "test@example.com"},
		{"config", "commit.gpgsign", "false"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	return dir
}

func TestRunUsesEnvDirInsteadOfProcessCwd(t *testing.T) {
	repo := newEmbeddedTestRepo(t)
	processCwd, err := os.Getwd()
	require.NoError(t, err)
	require.NotEqual(t, repo, processCwd)

	var stdout, stderr bytes.Buffer
	err = Run(t.Context(), []string{"wtp", "cd", "@"}, Env{
		Dir:    repo,
		Stdout: &stdout,
		Stderr: &stderr,
	})

	require.NoError(t, err, stderr.String())
	assert.Equal(t, repo+"\n", stdout.String())
}

func TestRunConcurrentInvocationsAreIsolated(t *testing.T) {
	repos := []string{newEmbeddedTestRepo(t), newEmbeddedTestRepo(t)}

	const iterations = 5
	var wg sync.WaitGroup
	type outcome struct {
		repo   string
		stdout string
		stderr string
		err    error
	}
	outcomes := make(chan outcome, len(repos)*iterations)

	for i := range iterations {
		for _, repo := range repos {
			wg.Go(func() {
				var stdout, stderr bytes.Buffer
				err := Run(t.Context(), []string{"wtp", "exec", "@", "--", "sh", "-c", "pwd; echo $WTP_RUN_ID"}, Env{
					Dir:     repo,
					Stdout:  &stdout,
					Stderr:  &stderr,
					Environ: append(os.Environ(), fmt.Sprintf("WTP_RUN_ID=%s-%d", filepath.Base(repo), i)),
				})
				outcomes <- outcome{repo: fmt.Sprintf("%s\n%s-%d\n", repo, filepath.Base(repo), i),
					stdout: stdout.String(), stderr: stderr.String(), err: err}
			})
		}
	}

	helpOutputs := make(chan string, iterations)
	for range iterations {
		wg.Go(func() {
			var stdout bytes.Buffer
			_ = Run(t.Context(), []string{"wtp", "cd", "--help"}, Env{Dir: repos[0], Stdout: &stdout, Stderr: &stdout})
			helpOutputs <- stdout.String()
		})
	}

	wg.Wait()
	close(outcomes)
	close(helpOutputs)

	for o := range outcomes {
		require.NoError(t, o.err, o.stderr)
		assert.Equal(t, o.repo, o.stdout)
	}
	for out := range helpOutputs {
		assert.Contains(t, out, "wtp cd")
	}
}

func TestRunShellInitInvokesSelfForCompletion(t *testing.T) {
	repo := newEmbeddedTestRepo(t)

	var stdout, stderr bytes.Buffer
	err := Run(t.Context(), []string{"wtp", "shell-init", "zsh"}, Env{
		Dir:     repo,
		Stdout:  &stdout,
		Stderr:  &stderr,
		Environ: append(os.Environ(), "WTP_SELF_MARKER=embedded"),
		Self:    []string{"sh", "-c", `echo "self:$0 $1 $2 $(pwd) $WTP_SELF_MARKER"`, "wtp"},
	})

	require.NoError(t, err, stderr.String())
	assert.True(t, strings.HasPrefix(stdout.String(), "self:wtp completion zsh "+repo+" embedded\n"), stdout.String())
	assert.Contains(t, stdout.String(), "# wtp shell hook for zsh")
}

func TestRunShellInitPassesSelfPrefixVerbatim(t *testing.T) {
	original := execCompletion
	t.Cleanup(func() { execCompletion = original })

	var gotArgv []string
	execCompletion = func(_ context.Context, argv []string) ([]byte, error) {
		gotArgv = argv
		return []byte("completion\n"), nil
	}

	var stdout bytes.Buffer
	err := Run(t.Context(), []string{"wtp", "shell-init", "bash"}, Env{
		Dir:    t.TempDir(),
		Stdout: &stdout,
		Stderr: &stdout,
		Self:   []string{"/path/to/anvil", "wtp"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"/path/to/anvil", "wtp", "completion", "bash"}, gotArgv)
}

func TestRunWithNonFileStreamsIsNotInteractive(t *testing.T) {
	repo := newEmbeddedTestRepo(t)

	t.Run("cd without argument skips the picker", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(t.Context(), []string{"wtp", "cd"}, Env{
			Dir:    repo,
			Stdin:  strings.NewReader(""),
			Stdout: &stdout,
			Stderr: &stderr,
		})

		require.NoError(t, err, stderr.String())
		assert.Equal(t, repo+"\n", stdout.String())
	})

	t.Run("exec captures output into the env stdout", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Run(t.Context(), []string{"wtp", "exec", "@", "--", "echo", "captured"}, Env{
			Dir:    repo,
			Stdin:  strings.NewReader(""),
			Stdout: &stdout,
			Stderr: &stderr,
		})

		require.NoError(t, err, stderr.String())
		assert.Equal(t, "captured\n", stdout.String())
	})
}

func TestRunReportsVersionFromEnv(t *testing.T) {
	var stdout bytes.Buffer
	require.NoError(t, Run(t.Context(), []string{"wtp", "--version"}, Env{
		Dir:     t.TempDir(),
		Stdout:  &stdout,
		Version: "v9.9.9",
	}))
	assert.Contains(t, stdout.String(), "v9.9.9")

	stdout.Reset()
	require.NoError(t, Run(t.Context(), []string{"wtp", "--version"}, Env{Dir: t.TempDir(), Stdout: &stdout}))
	assert.Contains(t, stdout.String(), defaultVersion)
}

func TestRunReturnsErrorsWithoutExiting(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(t.Context(), []string{"wtp", "completion", "nushell"}, Env{
		Dir:    t.TempDir(),
		Stdout: &stdout,
		Stderr: &stderr,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown shell nushell")
}
