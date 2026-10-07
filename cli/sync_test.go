package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/launchd"
	"github.com/Broderick-Westrope/wtp/v3/internal/maintenance"
	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
)

func isolateSyncDirs(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	axdg.Reload()
	t.Cleanup(axdg.Reload)
}

// initRepoWithOrigin creates a git repository whose origin is a GitHub URL.
func initRepoWithOrigin(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "https://github.com/owner/" + name + ".git"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return resolved
}

type fakeSyncer struct {
	result maintenance.Result
	err    error
}

func (f fakeSyncer) Sync(context.Context) (maintenance.Result, error) { return f.result, f.err }

// stubRunner replaces the per-repo syncer, recording which repos were synced.
func stubRunner(t *testing.T, byRepo map[string]fakeSyncer) *[]string {
	t.Helper()
	var synced []string
	orig := syncNewRunner
	syncNewRunner = func(main string, id *remote.RepoIdentifier, _ io.Writer) repoSyncer {
		synced = append(synced, id.StoragePath())
		return byRepo[filepath.Base(main)]
	}
	t.Cleanup(func() { syncNewRunner = orig })
	return &synced
}

func stubDiscover(t *testing.T, repos ...string) {
	t.Helper()
	orig := syncDiscoverRepos
	syncDiscoverRepos = func(string) ([]string, error) { return repos, nil }
	t.Cleanup(func() { syncDiscoverRepos = orig })
}

func TestRunSync_CurrentRepoPrintsArchives(t *testing.T) {
	isolateSyncDirs(t)
	repo := initRepoWithOrigin(t, "alpha")
	synced := stubRunner(t, map[string]fakeSyncer{"alpha": {result: maintenance.Result{
		Checked:  2,
		Archived: []maintenance.Archived{{Repo: "owner/alpha", Branch: "feat", PRNumber: 4, PRState: "MERGED"}},
	}}})

	var out bytes.Buffer
	require.NoError(t, runSync(withTestEnv(t.Context(), repo), &out, syncOptions{}))

	assert.Equal(t, []string{"owner/alpha"}, *synced)
	assert.Equal(t, "Auto-archived feat in owner/alpha (PR #4 MERGED)\n"+
		"Synced 2 worktree(s) in 1 repo(s): 1 archived\n", out.String())

	notices, err := maintenance.DrainNotices()
	require.NoError(t, err)
	assert.Empty(t, notices, "a sync the user watched has nothing left to tell them")

	_, ran := maintenance.LastFullSync()
	assert.False(t, ran, "a single-repo sync is not a full sync")
}

func TestRunSync_OutsideRepoNeedsAll(t *testing.T) {
	isolateSyncDirs(t)
	err := runSync(withTestEnv(t.Context(), t.TempDir()), io.Discard, syncOptions{})
	assert.ErrorContains(t, err, "use --all")
}

func TestRunSync_AllSyncsEveryRepoAndContinuesPastFailures(t *testing.T) {
	isolateSyncDirs(t)
	alpha := initRepoWithOrigin(t, "alpha")
	beta := initRepoWithOrigin(t, "beta")
	noOrigin := t.TempDir()
	stubDiscover(t, alpha, noOrigin, beta)
	synced := stubRunner(t, map[string]fakeSyncer{
		"alpha": {err: errors.New("boom")},
		"beta":  {result: maintenance.Result{Checked: 3, Failed: 1}},
	})

	var out bytes.Buffer
	ctx := withTestEnv(t.Context(), t.TempDir())
	require.NoError(t, runSync(ctx, &out, syncOptions{all: true}))

	assert.Equal(t, []string{"owner/alpha", "owner/beta"}, *synced)
	assert.Equal(t, "Synced 3 worktree(s) in 3 repo(s): 0 archived, 3 failed\n", out.String())
	_, ran := maintenance.LastFullSync()
	assert.True(t, ran)
}

func TestRunSync_BackgroundQueuesNotices(t *testing.T) {
	isolateSyncDirs(t)
	repo := initRepoWithOrigin(t, "alpha")
	stubRunner(t, map[string]fakeSyncer{"alpha": {result: maintenance.Result{
		Checked:  1,
		Archived: []maintenance.Archived{{Repo: "owner/alpha", Branch: "feat", PRNumber: 4, PRState: "CLOSED"}},
	}}})

	require.NoError(t, runSync(withTestEnv(t.Context(), repo), io.Discard, syncOptions{background: true}))

	notices, err := maintenance.DrainNotices()
	require.NoError(t, err)
	assert.Equal(t, []string{"Auto-archived feat in owner/alpha (PR #4 CLOSED)"}, notices)
}

func TestRunSync_ScheduledSkipsWhenNotDue(t *testing.T) {
	isolateSyncDirs(t)
	require.NoError(t, maintenance.MarkFullSync())
	synced := stubRunner(t, nil)
	stubDiscover(t, initRepoWithOrigin(t, "alpha"))

	var out bytes.Buffer
	opts := syncOptions{all: true, ifDue: true, background: true}
	require.NoError(t, runSync(withTestEnv(t.Context(), t.TempDir()), &out, opts))

	assert.Empty(t, *synced)
	assert.Empty(t, out.String(), "a tick with nothing due writes nothing to the log")
}

func TestRunSync_SkipsWhileAnotherSyncRuns(t *testing.T) {
	isolateSyncDirs(t)
	unlock, err := maintenance.AcquireLock()
	require.NoError(t, err)
	t.Cleanup(unlock)
	synced := stubRunner(t, nil)

	var out bytes.Buffer
	require.NoError(t, runSync(withTestEnv(t.Context(), t.TempDir()), &out, syncOptions{all: true}))

	assert.Empty(t, *synced)
	assert.Contains(t, out.String(), "already running")
}

type recordingLaunchctl struct{ bootstrapped string }

func (*recordingLaunchctl) Bootout(string) error { return nil }
func (r *recordingLaunchctl) Bootstrap(path string) error {
	r.bootstrapped = path
	return nil
}

func stubAgent(t *testing.T, goos string) (lc *recordingLaunchctl, plistPath string) {
	t.Helper()
	lc = &recordingLaunchctl{}
	plistPath = filepath.Join(t.TempDir(), launchd.Label+".plist")

	origGOOS, origLC, origPath := syncGOOS, syncLaunchctl, syncPlistPath
	syncGOOS, syncLaunchctl = goos, lc
	syncPlistPath = func() (string, error) { return plistPath, nil }
	t.Cleanup(func() { syncGOOS, syncLaunchctl, syncPlistPath = origGOOS, origLC, origPath })
	return lc, plistPath
}

func TestInstallSyncAgent_BakesBinaryAndEnvironment(t *testing.T) {
	isolateSyncDirs(t)
	lc, plistPath := stubAgent(t, "darwin")

	binDir := t.TempDir()
	binary := filepath.Join(binDir, "wtp")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))

	ctx := procenv.WithEnv(t.Context(), &procenv.Env{
		Self:    []string{binary},
		Environ: []string{"PATH=" + binDir + ":/usr/bin", "XDG_DATA_HOME=/custom/data", "GH_TOKEN=secret"},
	})
	var out bytes.Buffer
	require.NoError(t, installSyncAgent(ctx, &out))

	assert.Equal(t, plistPath, lc.bootstrapped)
	plist, err := launchd.Installed(plistPath)
	require.NoError(t, err)
	program, err := launchd.ProgramPath(plist)
	require.NoError(t, err)
	assert.Equal(t, binary, program)
	assert.Contains(t, string(plist), "<string>"+binDir+":/usr/bin</string>")
	assert.Contains(t, string(plist), "<string>/custom/data</string>")
	assert.NotContains(t, string(plist), "secret", "credentials are never written to the plist")
	assert.Contains(t, out.String(), "every 1h0m0s")
}

func TestInstallSyncAgent_RejectsEmbeddedSelf(t *testing.T) {
	isolateSyncDirs(t)
	stubAgent(t, "darwin")
	ctx := procenv.WithEnv(t.Context(), &procenv.Env{Self: []string{"/bin/host", "wtp"}, SelfExplicit: true})
	assert.ErrorContains(t, installSyncAgent(ctx, io.Discard), "standalone wtp binary")
}

func TestInstallSyncAgent_RequiresMacOS(t *testing.T) {
	stubAgent(t, "linux")
	assert.ErrorContains(t, installSyncAgent(t.Context(), io.Discard), "wtp sync --scheduled")
}

func TestCheckSyncAgent(t *testing.T) {
	isolateSyncDirs(t)
	_, plistPath := stubAgent(t, "darwin")

	var out bytes.Buffer
	assert.Equal(t, 1, checkSyncAgent(&out))
	assert.Contains(t, out.String(), "wtp sync --install")

	plist, err := launchd.Render("/missing/wtp", "/tmp/log", nil, 0)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(plistPath, plist, 0o600))
	out.Reset()
	assert.Equal(t, 1, checkSyncAgent(&out))
	assert.Contains(t, out.String(), "missing binary: /missing/wtp")

	plist, err = launchd.Render("/bin/sh", "/tmp/log", nil, 0)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(plistPath, plist, 0o600))
	require.NoError(t, maintenance.MarkFullSync())
	out.Reset()
	assert.Equal(t, 0, checkSyncAgent(&out))
	assert.Contains(t, out.String(), "✓ Background sync agent installed")

	old := time.Now().Add(-24 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(maintenance.SyncDir(), "last-run"), old, old))
	out.Reset()
	assert.Equal(t, 1, checkSyncAgent(&out))
	assert.Contains(t, out.String(), "Last background sync was")
}

func TestCheckSyncAgent_SkippedOffMacOS(t *testing.T) {
	stubAgent(t, "linux")
	assert.Zero(t, checkSyncAgent(io.Discard))
}
