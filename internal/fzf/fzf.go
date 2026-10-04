// Package fzf provides interactive fuzzy selection via the fzf command-line tool.
package fzf

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

// ErrCanceled is returned when the user cancels the fzf selection (Esc or Ctrl-C).
var ErrCanceled = errors.New("selection canceled")

// Finder selects an item from a list via interactive fuzzy matching.
type Finder interface {
	// Available reports whether fzf can be used: it is installed and on PATH
	// and the environment has a terminal to draw on.
	Available() bool

	// Find presents items via fzf and returns the selected item.
	// query pre-fills fzf's search input (pass "" for no initial query).
	// Returns ErrCanceled if the user dismisses the picker.
	Find(items []string, query string) (string, error)
}

// ExecFinder implements Finder by shelling out to the fzf binary.
type ExecFinder struct {
	env *procenv.Env
}

// NewFinder creates a Finder backed by the fzf binary that runs within env.
// A nil env uses the current process.
func NewFinder(env *procenv.Env) *ExecFinder {
	if env == nil {
		env = procenv.Default()
	}
	return &ExecFinder{env: env}
}

// fzfExitInterrupted is fzf's exit code for Ctrl-C / Esc.
const fzfExitInterrupted = 130

// Available reports whether fzf is installed and on PATH and the environment's
// stderr is a terminal fzf can draw on.
func (f *ExecFinder) Available() bool {
	if !procenv.IsTerminal(f.env.Stderr) {
		return false
	}
	_, err := exec.LookPath("fzf")
	return err == nil
}

// Find launches fzf with the given items and optional query.
// When query is non-empty it is pre-filled in fzf's search bar.
// If there is exactly one fuzzy match, fzf auto-selects it (--select-1).
func (f *ExecFinder) Find(items []string, query string) (string, error) {
	args := []string{
		"--select-1",
		"--height=~50%",
		"--layout=reverse",

		"--prompt", "worktree> ",
	}
	if query != "" {
		args = append(args, "--query", query)
	}

	cmd := exec.Command("fzf", args...)
	cmd.Stdin = strings.NewReader(strings.Join(items, "\n"))
	cmd.Stderr = f.env.Stderr
	cmd.Dir = f.env.Dir
	cmd.Env = f.env.Environ

	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// fzf exit codes: 1 = no match, 130 = Ctrl-C/Esc; treat both as user cancellation.
			// Exit code 2 = fzf error, falls through to the default error return.
			switch exitErr.ExitCode() {
			case 1, fzfExitInterrupted:
				return "", ErrCanceled
			}
		}
		return "", fmt.Errorf("fzf failed: %w", err)
	}

	selected := strings.TrimSpace(out.String())
	if selected == "" {
		return "", ErrCanceled
	}

	return selected, nil
}
