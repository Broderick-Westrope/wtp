package marker

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmit_WritesMarkerWhenHooked(t *testing.T) {
	environ := []string{"__WTP_HOOKED=1"}

	var buf bytes.Buffer
	err := Emit(&buf, environ, "/some/path/to/worktree")

	require.NoError(t, err)
	assert.Equal(t, "__wtp_cd:/some/path/to/worktree\n", buf.String())
}

func TestEmit_NoOutputWhenNotHooked(t *testing.T) {
	environ := []string{"__WTP_HOOKED="}

	var buf bytes.Buffer
	err := Emit(&buf, environ, "/some/path")

	require.NoError(t, err)
	assert.Empty(t, buf.String())
}

func TestEmit_IgnoresProcessEnvironment(t *testing.T) {
	t.Setenv("__WTP_HOOKED", "1")

	var buf bytes.Buffer
	err := Emit(&buf, nil, "/some/path")

	require.NoError(t, err)
	assert.Empty(t, buf.String())
}

func TestEmit_NoOutputWhenHookedIsWrongValue(t *testing.T) {
	environ := []string{"__WTP_HOOKED=0"}

	var buf bytes.Buffer
	err := Emit(&buf, environ, "/some/path")

	require.NoError(t, err)
	assert.Empty(t, buf.String())
}
