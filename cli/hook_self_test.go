package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var embeddedSelf = []string{"/path/to/my anvil", "wtp"}

func runWithSelf(t *testing.T, self []string, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(t.Context(), append([]string{"wtp"}, args...), Env{
		Dir:    t.TempDir(),
		Stdout: &stdout,
		Stderr: &stderr,
		Self:   self,
	})
	require.NoError(t, err, stderr.String())
	return stdout.String()
}

func TestHookScriptsUseExplicitSelfPrefix(t *testing.T) {
	tests := []struct {
		shell    string
		prefix   string
		function string
	}{
		{shell: "bash", prefix: "command '/path/to/my anvil' 'wtp' ", function: "wtp() {"},
		{shell: "zsh", prefix: "command '/path/to/my anvil' 'wtp' ", function: "wtp() {"},
		{shell: "fish", prefix: "command '/path/to/my anvil' 'wtp' ", function: "function wtp"},
	}

	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			out := runWithSelf(t, embeddedSelf, "hook", tt.shell)

			assert.Contains(t, out, tt.function)
			assert.Contains(t, out, "__WTP_HOOKED=1 "+tt.prefix)
			assert.Contains(t, out, tt.prefix+"cd")
			assert.NotContains(t, out, "command wtp ")
			assert.Equal(t, strings.Count(standaloneHook(t, tt.shell), "command wtp "), strings.Count(out, tt.prefix))
		})
	}
}

func TestHookScriptsWithoutSelfAreStandalone(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			out := runWithSelf(t, nil, "hook", shell)
			assert.Equal(t, standaloneHook(t, shell), out)
			assert.Contains(t, out, "command wtp ")
		})
	}
}

func standaloneHook(t *testing.T, shell string) string {
	t.Helper()
	var buf bytes.Buffer
	switch shell {
	case "bash":
		require.NoError(t, printBashHook(&buf))
	case "zsh":
		require.NoError(t, printZshHook(&buf))
	case "fish":
		require.NoError(t, printFishHook(&buf))
	}
	return buf.String()
}

func TestShellQuoting(t *testing.T) {
	assert.Equal(t, `'plain'`, posixQuote("plain"))
	assert.Equal(t, `'it'\''s'`, posixQuote("it's"))
	assert.Equal(t, `'a\b'`, posixQuote(`a\b`))
	assert.Equal(t, `'plain'`, fishQuote("plain"))
	assert.Equal(t, `'it\'s'`, fishQuote("it's"))
	assert.Equal(t, `'a\\b'`, fishQuote(`a\b`))
}

func TestCompletionScriptsUseExplicitSelfPrefix(t *testing.T) {
	t.Run("zsh", func(t *testing.T) {
		original := strings.ReplaceAll(readCompletionTestdata(t, "zsh_expected.zsh"), "env WTP_SHELL_COMPLETION=1 ", "")
		out := patchZshCompletionScript(original, embeddedSelf...)
		const invocation = `env WTP_SHELL_COMPLETION=1 '/path/to/my anvil' 'wtp' ${words[2,-2]} `
		assert.Contains(t, out, invocation+`${current} --generate-shell-completion`)
		assert.Contains(t, out, invocation+`--generate-shell-completion`)
		assert.NotContains(t, out, "${words[@]:0:#words[@]-1}")
		assert.Contains(t, out, "compdef _wtp wtp")
	})

	t.Run("fish", func(t *testing.T) {
		out := buildFishCompletionScript(embeddedSelf...)
		assert.Contains(t, out, "command -sq '/path/to/my anvil'\n")
		assert.Contains(t, out, "WTP_SHELL_COMPLETION=1 command '/path/to/my anvil' 'wtp' $args")
		assert.Contains(t, out, "complete -c wtp ")
		assert.NotContains(t, out, "command wtp ")
		assert.NotContains(t, out, "command -sq wtp")
	})
}

func TestBashHookReachesSelfEndToEnd(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}

	stub := filepath.Join(t.TempDir(), "my anvil")
	require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\necho \"stub:$* hooked=$__WTP_HOOKED\"\n"), 0o755))

	hook := runWithSelf(t, []string{stub, "wtp"}, "hook", "bash")

	cmd := exec.CommandContext(t.Context(), bash, "--norc", "--noprofile", "-c", `eval "$WTP_HOOK" && wtp list`)
	cmd.Env = append(os.Environ(), "WTP_HOOK="+hook, "PATH=/usr/bin:/bin")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, "stub:wtp list hooked=1\n", string(out))
}
