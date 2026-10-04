package cli

import (
	"context"
	"io"
	"time"

	"github.com/Broderick-Westrope/wtp/v3/internal/config"
	"github.com/Broderick-Westrope/wtp/v3/internal/maintenance"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
)

const maintenanceTimeout = 30 * time.Second

// Variables to allow mocking in tests.
var (
	maintGetwd      = getwd
	maintNewGitRepo = newRepository
)

// runMaintenance executes cheap and expensive maintenance for the current repo.
// Errors are non-fatal: the function always returns nil so the user's command proceeds.
func runMaintenance(ctx context.Context, w io.Writer) error {
	cwd, err := maintGetwd(ctx)
	if err != nil {
		return nil //nolint:nilerr // not in usable dir — skip silently
	}

	repo, err := maintNewGitRepo(ctx, cwd)
	if err != nil {
		return nil //nolint:nilerr // not in git repo — skip silently
	}

	mainRepoPath, err := repo.GetMainWorktreePath()
	if err != nil {
		return nil //nolint:nilerr // skip silently
	}

	remoteURL, err := repo.GetRemoteURL("origin")
	if err != nil {
		return nil //nolint:nilerr // no remote — skip silently
	}

	repoID, err := remote.Parse(remoteURL)
	if err != nil {
		return nil //nolint:nilerr // unparseable remote — skip silently
	}

	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return nil //nolint:nilerr // bad config — skip silently
	}

	stateStore := state.NewStore()
	runner := maintenance.NewRunner(stateStore, cfg, &repoID, mainRepoPath, w)

	_ = runner.RunCheap()

	expCtx, cancel := context.WithTimeout(ctx, maintenanceTimeout)
	defer cancel()

	_ = runner.RunExpensive(expCtx)

	return nil
}
