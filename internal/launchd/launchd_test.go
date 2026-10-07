package launchd_test

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/launchd"
)

var update = flag.Bool("update", false, "rewrite golden files")

func render(t *testing.T) []byte {
	t.Helper()
	plist, err := launchd.Render(
		"/Users/test/.local/bin/wtp",
		"/Users/test/Library/Application Support/wtp/sync/sync.log",
		[]launchd.EnvVar{
			{Key: "PATH", Value: "/opt/homebrew/bin:/usr/bin:/bin"},
			{Key: "XDG_DATA_HOME", Value: "/Users/test/data & stuff"},
		},
		0,
	)
	require.NoError(t, err)
	return plist
}

func TestRender_MatchesGolden(t *testing.T) {
	got := render(t)
	golden := filepath.Join("testdata", "plist.golden")
	if *update {
		require.NoError(t, os.WriteFile(golden, got, 0o600))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

func TestProgramPath_RoundTripsRender(t *testing.T) {
	path, err := launchd.ProgramPath(render(t))
	require.NoError(t, err)
	assert.Equal(t, "/Users/test/.local/bin/wtp", path)

	_, err = launchd.ProgramPath([]byte("<plist/>"))
	assert.Error(t, err)
}

type fakeLaunchctl struct {
	calls        []string
	bootstrapErr error
}

func (f *fakeLaunchctl) Bootout(label string) error {
	f.calls = append(f.calls, "bootout "+label)
	return errors.New("not loaded")
}

func (f *fakeLaunchctl) Bootstrap(path string) error {
	f.calls = append(f.calls, "bootstrap "+path)
	return f.bootstrapErr
}

func TestInstallAndUninstall(t *testing.T) {
	plistPath := filepath.Join(t.TempDir(), "LaunchAgents", launchd.Label+".plist")
	lc := &fakeLaunchctl{}

	require.NoError(t, launchd.Install(lc, plistPath, []byte("plist")))
	got, err := launchd.Installed(plistPath)
	require.NoError(t, err)
	assert.Equal(t, "plist", string(got))
	assert.Equal(t, []string{"bootout " + launchd.Label, "bootstrap " + plistPath}, lc.calls)

	require.NoError(t, launchd.Uninstall(lc, plistPath))
	_, err = launchd.Installed(plistPath)
	assert.ErrorIs(t, err, launchd.ErrNotInstalled)

	require.NoError(t, launchd.Uninstall(lc, plistPath), "uninstall is idempotent")
}

func TestInstall_ReportsBootstrapFailure(t *testing.T) {
	plistPath := filepath.Join(t.TempDir(), launchd.Label+".plist")
	err := launchd.Install(&fakeLaunchctl{bootstrapErr: errors.New("boom")}, plistPath, []byte("plist"))
	assert.ErrorContains(t, err, "boom")
}
