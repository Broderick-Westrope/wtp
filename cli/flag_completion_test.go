package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

func TestCompleteFlagSuggestions_MatchesLongFlag(t *testing.T) {
	var buf bytes.Buffer

	cmd := &cli.Command{
		Writer: &buf,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name: "keep-branch",
			},
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
			},
			cli.GenerateShellCompletionFlag,
		},
	}

	require.True(t, completeFlagSuggestions(t.Context(), cmd, "--k"))

	require.Contains(t, buf.String(), "--keep-branch")
	require.NotContains(t, buf.String(), "--generate-shell-completion")
}

func TestCompleteFlagSuggestions_ShowsAllForSingleHyphen(t *testing.T) {
	var buf bytes.Buffer

	cmd := &cli.Command{
		Writer: &buf,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name: "keep-branch",
			},
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
			},
		},
	}

	require.True(t, completeFlagSuggestions(t.Context(), cmd, "-"))

	output := buf.String()
	require.True(t, strings.Contains(output, "--keep-branch") || strings.Contains(output, "-keep-branch"))
	require.True(t, strings.Contains(output, "--force") || strings.Contains(output, "-force"))
}

func TestMaybeCompleteFlagSuggestions_IgnoresPreviousWhenCurrentEmpty(t *testing.T) {
	cmd := &cli.Command{
		Writer: io.Discard,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "force"},
			cli.GenerateShellCompletionFlag,
		},
	}

	require.False(t, maybeCompleteFlagSuggestions(t.Context(), cmd, "", []string{"--force"}))
}

func TestMaybeCompleteFlagSuggestions_IgnoresSentinelInPrevious(t *testing.T) {
	cmd := &cli.Command{
		Writer: io.Discard,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "force"},
			cli.GenerateShellCompletionFlag,
		},
	}

	require.False(t, maybeCompleteFlagSuggestions(t.Context(), cmd, "feature", []string{"-"}))
	require.False(t, maybeCompleteFlagSuggestions(t.Context(), cmd, "", []string{"-"}))
}

func TestFlagCandidateFromArgsSentinel(t *testing.T) {
	candidate, ok := flagCandidateFromArgs([]string{"wtp", "remove", "target", "-", "--generate-shell-completion"})
	require.True(t, ok)
	require.Equal(t, "target", candidate)

	candidate, ok = flagCandidateFromArgs([]string{"wtp", "remove", "target", "--", "--generate-shell-completion"})
	require.True(t, ok)
	require.Equal(t, "target", candidate)

	candidate, ok = flagCandidateFromArgs(
		[]string{"wtp", "remove", "target", "-", "--generate-shell-completion", "--generate-shell-completion"},
	)
	require.True(t, ok)
	require.Equal(t, "target", candidate)
}

func TestMaybeCompleteFlagSuggestions_UsesInvocationArgsWhenCurrentEmpty(t *testing.T) {
	ctx := procenv.WithEnv(t.Context(), &procenv.Env{
		Args: []string{"wtp", "remove", "--k", "--generate-shell-completion"},
	})

	var buf bytes.Buffer
	cmd := &cli.Command{
		Writer: &buf,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "keep-branch"},
			&cli.BoolFlag{Name: "force"},
			cli.GenerateShellCompletionFlag,
		},
	}

	require.True(t, maybeCompleteFlagSuggestions(ctx, cmd, "", nil))
	require.Contains(t, buf.String(), "--keep-branch")
}
