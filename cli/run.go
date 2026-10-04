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

	"github.com/urfave/cli/v3"
)

const defaultVersion = "dev"

// Env describes the process environment a wtp invocation runs in.
type Env struct {
	// Dir is the working directory; "" means os.Getwd().
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
	app := newApp()
	app.Version = env.Version
	if app.Version == "" {
		app.Version = defaultVersion
	}
	app.Reader = env.Stdin
	app.Writer = env.Stdout
	app.ErrWriter = env.Stderr
	app.ExitErrHandler = func(context.Context, *cli.Command, error) {}

	return app.Run(ctx, normalizeCompletionArgs(args))
}
