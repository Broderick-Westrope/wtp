package command

import (
	"os/exec"
	"strings"

	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

// realShellExecutor implements ShellExecutor using os/exec
type realShellExecutor struct {
	env *procenv.Env
}

// NewRealShellExecutor creates a new shell executor that executes real commands
// within env. Commands without a WorkDir run in env.Dir with env.Environ, and
// interactive commands use env's streams. A nil env uses the current process.
func NewRealShellExecutor(env *procenv.Env) ShellExecutor {
	if env == nil {
		env = procenv.Default()
	}
	return &realShellExecutor{env: env}
}

// Execute runs the command using os/exec
func (e *realShellExecutor) Execute(name string, args []string, workDir string, interactive bool) (string, error) {
	cmd := exec.Command(name, args...)

	cmd.Dir = e.env.Dir
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = e.env.Environ

	if interactive && e.env.Interactive() {
		cmd.Stdin = e.env.Stdin
		cmd.Stdout = e.env.Stdout
		cmd.Stderr = e.env.Stderr
		return "", cmd.Run()
	}

	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}
