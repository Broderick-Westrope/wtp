package state

import (
	"fmt"
	"os"
	"strings"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
)

// PerformArchive executes the write-first archive sequence: record metadata in state,
// then remove worktree, then delete branch. Used by both manual archive and auto-archive.
//
// On partial failure (state written but worktree removal or branch deletion fails),
// a warning is printed to stderr but no error is returned — the state is the source of truth.
func PerformArchive(executor command.Executor, stateStore *Store, key string, ws *WorktreeState) error {
	if err := stateStore.SetArchivedFull(key, ws); err != nil {
		return fmt.Errorf("set archived state: %w", err)
	}

	// Remove worktree — skip if already gone.
	if ws.WorktreePath != "" {
		removeCmd := command.GitWorktreeRemove(ws.WorktreePath, true)
		result, err := executor.Execute([]command.Command{removeCmd})
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to execute worktree remove: %v\n", err)
		} else if len(result.Results) > 0 && result.Results[0].Error != nil {
			errMsg := result.Results[0].Error.Error()
			output := result.Results[0].Output
			combined := errMsg + " " + output
			if !strings.Contains(combined, "not a valid working tree") &&
				!strings.Contains(combined, "is not a working tree") {
				_, _ = fmt.Fprintf(os.Stderr, "warning: worktree remove failed: %s\n", combined)
			}
		}
	}

	// Delete branch — skip if already gone.
	if ws.Branch != "" {
		deleteCmd := command.GitBranchForceDelete(ws.Branch)
		result, err := executor.Execute([]command.Command{deleteCmd})
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to execute branch delete: %v\n", err)
		} else if len(result.Results) > 0 && result.Results[0].Error != nil {
			errMsg := result.Results[0].Error.Error()
			output := result.Results[0].Output
			combined := errMsg + " " + output
			if !strings.Contains(combined, "not found") {
				_, _ = fmt.Fprintf(os.Stderr, "warning: branch delete failed: %s\n", combined)
			}
		}
	}

	return nil
}
