package cli

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

type dispatchKey struct{}

func TestRunAppExecutesBeforeAndActionOutsideParseLock(t *testing.T) {
	var calls []string
	lockFree := func() bool {
		if parseMu.TryLock() {
			parseMu.Unlock()
			return true
		}
		return false
	}

	app := &cli.Command{
		Name:      "wtp",
		Writer:    io.Discard,
		ErrWriter: io.Discard,
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			calls = append(calls, "before")
			assert.True(t, lockFree())
			return context.WithValue(ctx, dispatchKey{}, "from-before"), nil
		},
		Commands: []*cli.Command{{
			Name: "sub",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				calls = append(calls, "action:"+cmd.Args().First())
				assert.True(t, lockFree())
				assert.Equal(t, "from-before", ctx.Value(dispatchKey{}))
				return errors.New("action failed")
			},
		}},
	}

	err := runApp(t.Context(), app, []string{"wtp", "sub", "arg"})

	require.EqualError(t, err, "action failed")
	assert.Equal(t, []string{"before", "action:arg"}, calls)
}

func TestRunAppSkipsActionWhenHelpRequested(t *testing.T) {
	actionCalled := false
	app := &cli.Command{
		Name:      "wtp",
		Writer:    io.Discard,
		ErrWriter: io.Discard,
		Commands: []*cli.Command{{
			Name: "sub",
			Action: func(context.Context, *cli.Command) error {
				actionCalled = true
				return nil
			},
		}},
	}

	require.NoError(t, runApp(t.Context(), app, []string{"wtp", "sub", "--help"}))
	assert.False(t, actionCalled)
}
