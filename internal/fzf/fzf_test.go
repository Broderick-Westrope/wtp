package fzf

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

func TestExecFinder_Available(_ *testing.T) {
	f := NewFinder(nil)
	// We can't assert true/false since fzf may or may not be installed,
	// but we can verify it doesn't panic and returns a bool.
	_ = f.Available()
}

func TestNewFinder(t *testing.T) {
	f := NewFinder(nil)
	assert.NotNil(t, f)
}

func TestExecFinder_UnavailableWithoutTerminal(t *testing.T) {
	var buf bytes.Buffer
	f := NewFinder(&procenv.Env{Stdin: &buf, Stdout: &buf, Stderr: &buf})

	assert.False(t, f.Available())
}

func TestErrCanceled(t *testing.T) {
	assert.NotNil(t, ErrCanceled)
	assert.NotEmpty(t, ErrCanceled.Error())
	assert.Contains(t, ErrCanceled.Error(), "cancel")
}

func TestExecFinder_Find_BinaryNotFound(t *testing.T) {
	// Override PATH so the fzf binary cannot be resolved, causing cmd.Run to
	// return a path error (not an *exec.ExitError). The function must wrap
	// that as "fzf failed: …" and must NOT return ErrCanceled.
	t.Setenv("PATH", "/nonexistent/path")

	f := NewFinder(nil)
	_, err := f.Find([]string{"item1", "item2"}, "")

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrCanceled)
	assert.ErrorContains(t, err, "fzf failed")
}
