// Package cli implements the wtp command-line interface as an embeddable library.
//
// The standalone wtp binary is a thin wrapper around [Run]. Other Go programs can
// embed wtp by calling [Run] with an [Env] describing the working directory,
// standard streams and environment of the invocation. Run keeps no per-invocation
// state in package variables, so concurrent calls with different environments are safe.
package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

const defaultVersion = "dev"

// Env describes the process environment a wtp invocation runs in.
type Env struct {
	// Dir is the working directory; "" means os.Getwd(). Relative paths are made absolute.
	Dir string
	// Stdin is the input stream; nil means os.Stdin.
	Stdin io.Reader
	// Stdout is the output stream; nil means os.Stdout.
	Stdout io.Writer
	// Stderr is the error stream; nil means os.Stderr.
	Stderr io.Writer
	// Environ is the environment in "KEY=value" form; nil means os.Environ().
	Environ []string
	// Self is the argv prefix that re-invokes this wtp; nil means []string{os.Executable()}.
	Self []string
	// Version is the version reported by wtp; "" means "dev".
	Version string
}

// Run executes wtp with args, where args[0] is the program name.
func Run(ctx context.Context, args []string, env Env) error { //nolint:gocritic // Env is an API value type
	resolved := env.resolve(args)
	ctx = procenv.WithEnv(ctx, resolved)

	app := newApp()
	app.Version = env.Version
	if app.Version == "" {
		app.Version = defaultVersion
	}
	app.Reader = resolved.Stdin
	app.Writer = resolved.Stdout
	app.ErrWriter = resolved.Stderr
	app.ExitErrHandler = func(context.Context, *cli.Command, error) {}

	return runApp(ctx, app, normalizeCompletionArgs(ctx, args))
}

func (e *Env) resolve(args []string) *procenv.Env {
	resolved := &procenv.Env{
		Dir:     e.Dir,
		Stdin:   e.Stdin,
		Stdout:  e.Stdout,
		Stderr:  e.Stderr,
		Environ: e.Environ,
		Self:    e.Self,
		Args:    slices.Clone(args),
	}
	if resolved.Dir == "" {
		resolved.Dir, resolved.DirErr = os.Getwd()
	} else if abs, err := filepath.Abs(resolved.Dir); err == nil {
		resolved.Dir = abs
	}
	if resolved.Stdin == nil {
		resolved.Stdin = os.Stdin
	}
	if resolved.Stdout == nil {
		resolved.Stdout = os.Stdout
	}
	if resolved.Stderr == nil {
		resolved.Stderr = os.Stderr
	}
	if resolved.Environ == nil {
		resolved.Environ = os.Environ()
	} else {
		resolved.Environ = slices.Clone(resolved.Environ)
	}
	if resolved.Self == nil {
		resolved.Self = procenv.DefaultSelf()
	} else {
		resolved.Self = slices.Clone(resolved.Self)
	}
	return resolved
}
