package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/Broderick-Westrope/wtp/v3/internal/config"
	"github.com/Broderick-Westrope/wtp/v3/internal/maintenance"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
)

// runMaintenance is the only maintenance done before an interactive command.
// It never touches the network or runs git: it prints notices queued by
// background syncs (one stat when there are none) and reaps expired archive
// entries (one state file read when none are due). GitHub checks happen in
// `wtp sync`.
var runMaintenance = func(_ context.Context, w io.Writer) error {
	if notices, err := maintenance.DrainNotices(); err == nil {
		for _, notice := range notices {
			_, _ = fmt.Fprintln(w, notice)
		}
	}

	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return nil //nolint:nilerr // bad config — skip silently
	}
	_ = maintenance.Reap(state.NewStore(), cfg.ArchiveRetention, w)
	return nil
}

// resolveSyncRepo returns the main worktree path and remote identity of the
// repository containing path.
func resolveSyncRepo(ctx context.Context, path string) (string, *remote.RepoIdentifier, error) {
	repo, err := newRepository(ctx, path)
	if err != nil {
		return "", nil, err
	}
	mainRepoPath, err := repo.GetMainWorktreePath()
	if err != nil {
		return "", nil, fmt.Errorf("resolving main worktree: %w", err)
	}
	remoteURL, err := repo.GetRemoteURL("origin")
	if err != nil {
		return "", nil, fmt.Errorf("no origin remote: %w", err)
	}
	repoID, err := remote.Parse(remoteURL)
	if err != nil {
		return "", nil, err
	}
	return mainRepoPath, &repoID, nil
}
