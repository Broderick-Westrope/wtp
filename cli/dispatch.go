package cli

import (
	"context"
	"sync"

	"github.com/urfave/cli/v3"
)

// parseMu serializes urfave/cli argument parsing: urfave mutates its
// package-global HelpFlag while setting up and parsing every command, so
// concurrent app.Run calls race. Before hooks and actions are deferred until
// parsing is done so that long-running commands do not hold the lock.
var parseMu sync.Mutex

type deferredExecution struct {
	befores []func(context.Context) (context.Context, error)
	action  func(context.Context) error
}

func (d *deferredExecution) wrap(cmd *cli.Command) {
	if before := cmd.Before; before != nil {
		cmd.Before = func(ctx context.Context, c *cli.Command) (context.Context, error) {
			d.befores = append(d.befores, func(ctx context.Context) (context.Context, error) {
				return before(ctx, c)
			})
			return ctx, nil
		}
	}
	if action := cmd.Action; action != nil {
		cmd.Action = func(_ context.Context, c *cli.Command) error {
			d.action = func(ctx context.Context) error {
				return action(ctx, c)
			}
			return nil
		}
	}
	for _, sub := range cmd.Commands {
		d.wrap(sub)
	}
}

func (d *deferredExecution) run(ctx context.Context, parseErr error) error {
	for _, before := range d.befores {
		next, err := before(ctx)
		if err != nil {
			return err
		}
		if next != nil {
			ctx = next
		}
	}
	if parseErr != nil {
		return parseErr
	}
	if d.action != nil {
		return d.action(ctx)
	}
	return nil
}

func runApp(ctx context.Context, app *cli.Command, args []string) error {
	var deferred deferredExecution
	deferred.wrap(app)

	parseErr := func() error {
		parseMu.Lock()
		defer parseMu.Unlock()
		return app.Run(ctx, args)
	}()

	return deferred.run(ctx, parseErr)
}
