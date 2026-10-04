package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

// newHookCommand creates the hook command definition
func newHookCommand() *cli.Command {
	return &cli.Command{
		Name:  "hook",
		Usage: "Generate shell hook for cd and auto-cd functionality",
		Description: "Generate shell hook scripts that enable the 'wtp cd' command to change directories " +
			"and auto-cd after 'wtp add'. " +
			"This provides a seamless navigation experience without needing subshells.\n\n" +
			"To enable the hook, add the following to your shell config:\n" +
			"  Bash (~/.bashrc):         eval \"$(wtp hook bash)\"\n" +
			"  Zsh (~/.zshrc):           eval \"$(wtp hook zsh)\"\n" +
			"  Fish (~/.config/fish/config.fish): wtp hook fish | source",
		Commands: []*cli.Command{
			{
				Name:        "bash",
				Usage:       "Generate bash hook script",
				Description: "Generate bash hook script for cd functionality",
				Action:      hookBash,
			},
			{
				Name:        "zsh",
				Usage:       "Generate zsh hook script",
				Description: "Generate zsh hook script for cd functionality",
				Action:      hookZsh,
			},
			{
				Name:        "fish",
				Usage:       "Generate fish hook script",
				Description: "Generate fish hook script for cd functionality",
				Action:      hookFish,
			},
		},
	}
}

func hookBash(ctx context.Context, cmd *cli.Command) error {
	w := stdoutFor(ctx, cmd)
	return printBashHook(w, scriptSelf(ctx)...)
}

func hookZsh(ctx context.Context, cmd *cli.Command) error {
	w := stdoutFor(ctx, cmd)
	return printZshHook(w, scriptSelf(ctx)...)
}

func hookFish(ctx context.Context, cmd *cli.Command) error {
	w := stdoutFor(ctx, cmd)
	return printFishHook(w, scriptSelf(ctx)...)
}

const standaloneInvocation = "command wtp "

func scriptSelf(ctx context.Context) []string {
	env := procenv.From(ctx)
	if !env.SelfExplicit {
		return nil
	}
	return env.Self
}

func posixQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func fishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func quoteArgv(argv []string, quote func(string) string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = quote(arg)
	}
	return strings.Join(quoted, " ")
}

func withInvocation(script string, self []string, quote func(string) string) string {
	if len(self) == 0 {
		return script
	}
	return strings.ReplaceAll(script, standaloneInvocation, "command "+quoteArgv(self, quote)+" ")
}

func printBashHook(w io.Writer, self ...string) error {
	_, err := io.WriteString(w, withInvocation(bashHookScript, self, posixQuote))
	return err
}

func printZshHook(w io.Writer, self ...string) error {
	_, err := io.WriteString(w, withInvocation(zshHookScript, self, posixQuote))
	return err
}

func printFishHook(w io.Writer, self ...string) error {
	_, err := fmt.Fprintln(w, withInvocation(fishHookScript, self, fishQuote))
	return err
}

const bashHookScript = `# wtp shell hook for bash
wtp() {
    for arg in "$@"; do
        if [[ "$arg" == "--generate-shell-completion" ]]; then
            command wtp "$@"
            return $?
        fi
    done
    if [[ "$1" == "cd" ]]; then
        local target_dir
        if [[ -z "$2" ]]; then
            target_dir=$(command wtp cd 2>/dev/null)
        else
            target_dir=$(command wtp cd "$2" 2>/dev/null)
        fi
        if [[ $? -eq 0 && -n "$target_dir" ]]; then
            cd "$target_dir"
        else
            if [[ -z "$2" ]]; then
                command wtp cd
            else
                command wtp cd "$2"
            fi
        fi
    else
        local __wtp_stderr_file
        __wtp_stderr_file=$(mktemp)
        __WTP_HOOKED=1 command wtp "$@" 2>"$__wtp_stderr_file"
        local __wtp_exit=$?
        local __wtp_target=""
        while IFS= read -r __wtp_line; do
            if [[ "$__wtp_line" == __wtp_cd:* ]]; then
                __wtp_target="${__wtp_line#__wtp_cd:}"
            else
                printf '%s\n' "$__wtp_line" >&2
            fi
        done < "$__wtp_stderr_file"
        rm -f "$__wtp_stderr_file"
        if [[ -n "$__wtp_target" && -d "$__wtp_target" ]]; then
            cd "$__wtp_target" || true
        fi
        return $__wtp_exit
    fi
}
`

const zshHookScript = `# wtp shell hook for zsh
wtp() {
    for arg in "$@"; do
        if [[ "$arg" == "--generate-shell-completion" ]]; then
            command wtp "$@"
            return $?
        fi
    done
    if [[ "$1" == "cd" ]]; then
        local target_dir
        if [[ -z "$2" ]]; then
            target_dir=$(command wtp cd 2>/dev/null)
        else
            target_dir=$(command wtp cd "$2" 2>/dev/null)
        fi
        if [[ $? -eq 0 && -n "$target_dir" ]]; then
            cd "$target_dir"
        else
            if [[ -z "$2" ]]; then
                command wtp cd
            else
                command wtp cd "$2"
            fi
        fi
    else
        local __wtp_stderr_file
        __wtp_stderr_file=$(mktemp)
        __WTP_HOOKED=1 command wtp "$@" 2>"$__wtp_stderr_file"
        local __wtp_exit=$?
        local __wtp_target=""
        while IFS= read -r __wtp_line; do
            if [[ "$__wtp_line" == __wtp_cd:* ]]; then
                __wtp_target="${__wtp_line#__wtp_cd:}"
            else
                printf '%s\n' "$__wtp_line" >&2
            fi
        done < "$__wtp_stderr_file"
        rm -f "$__wtp_stderr_file"
        if [[ -n "$__wtp_target" && -d "$__wtp_target" ]]; then
            cd "$__wtp_target" || true
        fi
        return $__wtp_exit
    fi
}
`

const fishHookScript = `# wtp shell hook for fish
function wtp
    for arg in $argv
        if test "$arg" = "--generate-shell-completion"
            command wtp $argv
            return $status
        end
    end
    if test "$argv[1]" = "cd"
        set -l target_dir
        if test -z "$argv[2]"
            set target_dir (command wtp cd 2>/dev/null)
        else
            set target_dir (command wtp cd $argv[2] 2>/dev/null)
        end
        if test $status -eq 0 -a -n "$target_dir"
            cd "$target_dir"
        else
            if test -z "$argv[2]"
                command wtp cd
            else
                command wtp cd $argv[2]
            end
        end
    else
        set -l __wtp_stderr_file (mktemp)
        __WTP_HOOKED=1 command wtp $argv 2>"$__wtp_stderr_file"
        set -l __wtp_exit $status
        set -l __wtp_target ""
        while read -l __wtp_line
            if string match -q '__wtp_cd:*' -- $__wtp_line
                set __wtp_target (string replace '__wtp_cd:' '' -- $__wtp_line)
            else
                echo "$__wtp_line" >&2
            end
        end < $__wtp_stderr_file
        rm -f $__wtp_stderr_file
        if test -n "$__wtp_target" -a -d "$__wtp_target"
            cd "$__wtp_target"
        end
        return $__wtp_exit
    end
end`
