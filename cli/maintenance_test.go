package cli

import (
	"bytes"
	"testing"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/maintenance"
)

func TestRunMaintenance_PrintsQueuedNoticesOnce(t *testing.T) {
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	axdg.Reload()

	require.NoError(t, maintenance.QueueNotices([]string{"Auto-archived feat in owner/repo (PR #1 MERGED)"}))

	var buf bytes.Buffer
	require.NoError(t, runMaintenance(t.Context(), &buf))
	assert.Equal(t, "Auto-archived feat in owner/repo (PR #1 MERGED)\n", buf.String())

	buf.Reset()
	require.NoError(t, runMaintenance(t.Context(), &buf))
	assert.Empty(t, buf.String())
}
