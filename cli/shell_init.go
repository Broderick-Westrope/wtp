package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"

	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

var allowedShells = map[string]struct{}{
	"bash": {},
	"zsh":  {},
	"fish": {},
}

var execCompletion = func(ctx context.Context, argv []string) ([]byte, error) {
	env := procenv.From(ctx)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = env.Dir
	cmd.Env = env.Environ
	return cmd.Output()
}

func runCompletionCommand(ctx context.Context, shell string) ([]byte, error) {
	if _, ok := allowedShells[shell]; !ok {
		return nil, fmt.Errorf("unsupported shell: %s", shell)
	}

	self := procenv.From(ctx).Self
	if len(self) == 0 {
		self = []string{"wtp"}
	}

	argv := append(slices.Clone(self), "completion", shell)
	return execCompletion(ctx, argv)
}

// NewShellInitCommand creates the shell-init command definition
func NewShellInitCommand() *cli.Command {
	return &cli.Command{
		Name:  "shell-init",
		Usage: "Initialize shell with completion and cd functionality",
		Description: "Generate shell initialization script that sets up both tab completion and cd functionality. " +
			"This is a convenience command that combines 'wtp completion' and 'wtp hook'.\n\n" +
			"To enable full shell integration, add the following to your shell config:\n" +
			"  Bash (~/.bashrc):         eval \"$(wtp shell-init bash)\"\n" +
			"  Zsh (~/.zshrc):           eval \"$(wtp shell-init zsh)\"\n" +
			"  Fish (~/.config/fish/config.fish): wtp shell-init fish | source",
		Commands: []*cli.Command{
			{
				Name:        "bash",
				Usage:       "Generate bash initialization script",
				Description: "Generate bash initialization script with completion and cd functionality",
				Action:      shellInitBash,
			},
			{
				Name:        "zsh",
				Usage:       "Generate zsh initialization script",
				Description: "Generate zsh initialization script with completion and cd functionality",
				Action:      shellInitZsh,
			},
			{
				Name:        "fish",
				Usage:       "Generate fish initialization script",
				Description: "Generate fish initialization script with completion and cd functionality",
				Action:      shellInitFish,
			},
		},
	}
}

func shellInitBash(ctx context.Context, cmd *cli.Command) error {
	w := stdoutFor(ctx, cmd)

	// Output completion first
	if err := outputCompletion(ctx, w, "bash"); err != nil {
		return err
	}

	// Then output hook
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	return printBashHook(w)
}

func shellInitZsh(ctx context.Context, cmd *cli.Command) error {
	w := stdoutFor(ctx, cmd)

	// Output completion first
	if err := outputCompletion(ctx, w, "zsh"); err != nil {
		return err
	}

	// Then output hook
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	return printZshHook(w)
}

func shellInitFish(ctx context.Context, cmd *cli.Command) error {
	w := stdoutFor(ctx, cmd)

	// Output completion first
	if err := outputCompletion(ctx, w, "fish"); err != nil {
		return err
	}

	// Then output hook
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	return printFishHook(w)
}

// outputCompletion executes wtp completion command and writes output to w
func outputCompletion(ctx context.Context, w io.Writer, shell string) error {
	output, err := runCompletionCommand(ctx, shell)
	if err != nil {
		return fmt.Errorf("failed to generate %s completion: %w", shell, err)
	}

	_, err = w.Write(output)
	return err
}
