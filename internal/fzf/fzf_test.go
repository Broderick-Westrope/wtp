package fzf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExecFinder_Available(_ *testing.T) {
	f := NewFinder()
	// We can't assert true/false since fzf may or may not be installed,
	// but we can verify it doesn't panic and returns a bool.
	_ = f.Available()
}

func TestNewFinder(t *testing.T) {
	f := NewFinder()
	assert.NotNil(t, f)
}
