// Package fzf provides interactive fuzzy selection via the fzf command-line tool.
package fzf

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrCanceled is returned when the user cancels the fzf selection (Esc or Ctrl-C).
var ErrCanceled = errors.New("selection canceled")

// Finder selects an item from a list via interactive fuzzy matching.
type Finder interface {
	// Available reports whether fzf is installed and on PATH.
	Available() bool

	// Find presents items via fzf and returns the selected item.
	// query pre-fills fzf's search input (pass "" for no initial query).
	// Returns ErrCanceled if the user dismisses the picker.
	Find(items []string, query string) (string, error)
}

// ExecFinder implements Finder by shelling out to the fzf binary.
type ExecFinder struct{}

// NewFinder creates a Finder backed by the fzf binary.
func NewFinder() *ExecFinder {
	return &ExecFinder{}
}

// fzfExitInterrupted is fzf's exit code for Ctrl-C / Esc.
const fzfExitInterrupted = 130

// Available reports whether fzf is installed and on PATH.
func (*ExecFinder) Available() bool {
	_, err := exec.LookPath("fzf")
	return err == nil
}

// Find launches fzf with the given items and optional query.
// When query is non-empty it is pre-filled in fzf's search bar.
// If there is exactly one fuzzy match, fzf auto-selects it (--select-1).
func (*ExecFinder) Find(items []string, query string) (string, error) {
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
	cmd.Stderr = os.Stderr

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
