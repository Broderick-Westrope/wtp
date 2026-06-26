package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Broderick-Westrope/wtp/v3/internal/git"
)

func TestRunMaintenance_SilentlySkipsNonGitDir(t *testing.T) {
	dir := t.TempDir()

	origGetwd := maintGetwd
	maintGetwd = func() (string, error) { return dir, nil }
	t.Cleanup(func() { maintGetwd = origGetwd })

	origNewGitRepo := maintNewGitRepo
	maintNewGitRepo = git.NewRepository // will fail on tmpdir
	t.Cleanup(func() { maintNewGitRepo = origNewGitRepo })

	var buf nopWriter
	err := runMaintenance(t.Context(), &buf)
	assert.NoError(t, err)
}

func TestRunMaintenance_SilentlySkipsNoRemote(t *testing.T) {
	// Create a bare git repo with no remote
	dir := t.TempDir()

	origGetwd := maintGetwd
	maintGetwd = func() (string, error) { return dir, nil }
	t.Cleanup(func() { maintGetwd = origGetwd })

	origNewGitRepo := maintNewGitRepo
	maintNewGitRepo = func(_ string) (*git.Repository, error) {
		// Return a repo backed by a path that has no origin remote
		return git.NewRepository(dir)
	}
	t.Cleanup(func() { maintNewGitRepo = origNewGitRepo })

	var buf nopWriter
	err := runMaintenance(t.Context(), &buf)
	assert.NoError(t, err)
}

// nopWriter discards all writes.
type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
