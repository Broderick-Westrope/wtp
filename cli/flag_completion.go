package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

const (
	maxHyphenPrefix   = 2
	sentinelArgOffset = 2
)

func completeFlagSuggestions(ctx context.Context, cmd *cli.Command, current string) bool {
	if cmd == nil {
		return false
	}

	writer := stdoutFor(ctx, cmd)

	trimmed, doubleDash := normalizeCurrent(current)
	if trimmed == "" && !strings.HasPrefix(current, "-") {
		return false
	}

	seen := make(map[string]struct{})
	emitted := false

	for _, flag := range cmd.Flags {
		if !isFlagVisible(flag) {
			continue
		}

		match := selectMatchingName(flag.Names(), trimmed, doubleDash)
		if match == "" {
			continue
		}

		completion := formatCompletion(match)
		if _, exists := seen[completion]; exists {
			continue
		}

		seen[completion] = struct{}{}
		if _, err := fmt.Fprintln(writer, completion); err != nil {
			return false
		}
		emitted = true
	}

	return emitted
}

func normalizeCurrent(current string) (trimmed string, doubleDash bool) {
	return strings.TrimLeft(current, "-"), strings.HasPrefix(current, "--")
}

func isFlagVisible(flag cli.Flag) bool {
	if visibility, ok := flag.(interface{ IsVisible() bool }); ok && !visibility.IsVisible() {
		return false
	}
	return true
}

func selectMatchingName(names []string, trimmed string, doubleDash bool) string {
	for _, candidate := range names {
		name := strings.TrimSpace(candidate)
		if name == "" {
			continue
		}

		if doubleDash && utf8.RuneCountInString(name) == 1 {
			continue
		}

		if trimmed != "" && !strings.HasPrefix(name, trimmed) {
			continue
		}

		if trimmed == name {
			continue
		}

		return name
	}

	return ""
}

func formatCompletion(name string) string {
	count := utf8.RuneCountInString(name)
	if count > maxHyphenPrefix {
		count = maxHyphenPrefix
	}
	return strings.Repeat("-", count) + name
}

func tryFlagCompletion(ctx context.Context, cmd *cli.Command, candidate string) bool {
	if strings.HasPrefix(candidate, "-") {
		return completeFlagSuggestions(ctx, cmd, candidate)
	}
	return false
}

func maybeCompleteFlagSuggestions(ctx context.Context, cmd *cli.Command, current string, previous []string) bool {
	currentNormalized := strings.TrimSuffix(current, "*")
	if currentNormalized != "" && tryFlagCompletion(ctx, cmd, currentNormalized) {
		return true
	}

	if len(previous) > 0 {
		last := strings.TrimSuffix(previous[len(previous)-1], "*")
		// Sentinel separating flags from positionals; ignore for flag completion.
		if last != "" && last != "-" && last != "--" && last != currentNormalized && tryFlagCompletion(ctx, cmd, last) {
			return true
		}
	}

	if candidate, ok := flagCandidateFromArgs(procenv.From(ctx).Args); ok {
		if candidate != "" && candidate != currentNormalized && tryFlagCompletion(ctx, cmd, candidate) {
			return true
		}
	}

	return false
}

func flagCandidateFromArgs(args []string) (string, bool) {
	preceding := args[:max(slices.Index(args, completionFlag), 0)]
	if len(preceding) == 0 {
		return "", false
	}

	candidate := preceding[len(preceding)-1]
	if candidate == "-" || candidate == "--" {
		if len(preceding) < sentinelArgOffset {
			return "", false
		}
		candidate = preceding[len(preceding)-sentinelArgOffset]
	}

	if candidate == completionFlag {
		return "", false
	}

	return candidate, candidate != ""
}
