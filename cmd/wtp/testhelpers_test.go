package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// RunWriterCommonTests runs a common pair of tests for functions that write
// to an io.Writer and may interact with a Git repo. It validates that the
// function does not panic in non-repo contexts and when a bare .git dir exists.
func RunWriterCommonTests(t *testing.T, name string, fn func(io.Writer) error) {
	t.Helper()

	t.Run(name+": should write to writer without panic", func(t *testing.T) {
		var buf bytes.Buffer
		assert.NotPanics(t, func() { _ = fn(&buf) })
	})

	t.Run(name+": should handle git directory gracefully", func(t *testing.T) {
		tempDir := t.TempDir()
		gitDir := filepath.Join(tempDir, ".git")
		assert.NoError(t, os.MkdirAll(gitDir, 0o755))

		oldDir, _ := os.Getwd()
		t.Cleanup(func() { _ = os.Chdir(oldDir) })
		assert.NoError(t, os.Chdir(tempDir))

		var buf bytes.Buffer
		assert.NotPanics(t, func() { _ = fn(&buf) })
	})
}
