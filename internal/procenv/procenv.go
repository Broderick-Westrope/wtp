// Package procenv carries the process environment (working directory, standard
// streams, environment variables and self-invocation argv) of a single wtp
// invocation, so that wtp can run embedded in another program without relying
// on process-global state.
package procenv

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Env is the resolved environment of a wtp invocation.
type Env struct {
	// Dir is the absolute working directory. It is empty when DirErr is set.
	Dir string
	// DirErr records why the working directory could not be determined.
	DirErr error
	// Stdin is the input stream.
	Stdin io.Reader
	// Stdout is the output stream.
	Stdout io.Writer
	// Stderr is the error stream.
	Stderr io.Writer
	// Environ is the environment in "KEY=value" form.
	Environ []string
	// Self is the argv prefix that re-invokes this wtp.
	Self []string
	// SelfExplicit reports whether Self was supplied by an embedder rather than
	// derived from the running executable.
	SelfExplicit bool
	// Args is the full argv of the invocation, including the program name.
	Args []string
}

type ctxKey struct{}

// ErrNoDir is returned by Getwd when no working directory is known.
var ErrNoDir = errors.New("working directory unknown")

// Default returns an Env derived from the current process.
func Default() *Env {
	dir, dirErr := os.Getwd()
	return &Env{
		Dir:     dir,
		DirErr:  dirErr,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Environ: os.Environ(),
		Self:    DefaultSelf(),
		Args:    os.Args,
	}
}

// DefaultSelf returns the argv prefix that re-invokes the running executable.
func DefaultSelf() []string {
	exe, err := os.Executable()
	if err != nil {
		exe = "wtp"
	}
	return []string{exe}
}

// WithEnv returns a copy of ctx carrying env. env must not be modified afterwards.
func WithEnv(ctx context.Context, env *Env) context.Context {
	return context.WithValue(ctx, ctxKey{}, env)
}

// From returns the Env carried by ctx, or Default() when ctx carries none.
// The returned Env is shared and must not be modified.
func From(ctx context.Context) *Env {
	if ctx != nil {
		if env, ok := ctx.Value(ctxKey{}).(*Env); ok && env != nil {
			return env
		}
	}
	return Default()
}

// Getwd returns the working directory of the invocation.
func (e *Env) Getwd() (string, error) {
	if e.DirErr != nil {
		return "", e.DirErr
	}
	if e.Dir == "" {
		return "", ErrNoDir
	}
	return e.Dir, nil
}

// Getenv returns the value of key in the invocation environment, or "" if unset.
func (e *Env) Getenv(key string) string {
	return Lookup(e.Environ, key)
}

// Interactive reports whether stdin, stdout and stderr are all terminals.
func (e *Env) Interactive() bool {
	return IsTerminal(e.Stdin) && IsTerminal(e.Stdout) && IsTerminal(e.Stderr)
}

// Lookup returns the value of key in environ, or "" if unset. Later entries
// take precedence, matching os/exec semantics for duplicate keys.
func Lookup(environ []string, key string) string {
	prefix := key + "="
	for i := len(environ) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(environ[i], prefix); ok {
			return value
		}
	}
	return ""
}

// IsTerminal reports whether stream is an *os.File connected to a terminal.
func IsTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	if !ok || f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
