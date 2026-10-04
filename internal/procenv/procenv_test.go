package procenv

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupLastEntryWins(t *testing.T) {
	environ := []string{"A=1", "B=2", "A=3", "AB=4"}

	assert.Equal(t, "3", Lookup(environ, "A"))
	assert.Equal(t, "2", Lookup(environ, "B"))
	assert.Equal(t, "4", Lookup(environ, "AB"))
	assert.Empty(t, Lookup(environ, "C"))
}

func TestFromReturnsCarriedEnv(t *testing.T) {
	env := &Env{Dir: "/repo", Environ: []string{"X=y"}}
	ctx := WithEnv(context.Background(), env)

	got := From(ctx)

	assert.Same(t, env, got)
	assert.Equal(t, "y", got.Getenv("X"))
}

func TestFromFallsBackToProcess(t *testing.T) {
	got := From(context.Background())

	require.NotNil(t, got)
	assert.NotNil(t, got.Stdout)
	assert.NotEmpty(t, got.Self)
}

func TestGetwd(t *testing.T) {
	dir, err := (&Env{Dir: "/repo"}).Getwd()
	require.NoError(t, err)
	assert.Equal(t, "/repo", dir)

	_, err = (&Env{}).Getwd()
	assert.ErrorIs(t, err, ErrNoDir)

	dirErr := errors.New("gone")
	_, err = (&Env{DirErr: dirErr}).Getwd()
	assert.ErrorIs(t, err, dirErr)
}

func TestNonFileStreamsAreNotInteractive(t *testing.T) {
	var buf bytes.Buffer
	env := &Env{Stdin: &buf, Stdout: &buf, Stderr: &buf}

	assert.False(t, IsTerminal(&buf))
	assert.False(t, IsTerminal(nil))
	assert.False(t, env.Interactive())
}
