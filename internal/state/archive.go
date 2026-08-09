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
// Returns (true, nil) when state was written and git cleanup fully succeeded.
// Returns (false, nil) when state was written but one or more git operations
// failed — warnings are printed to stderr. The caller should suggest retrying.
// Returns (false, err) only when the state write itself fails (fatal).
func PerformArchive(executor command.Executor, stateStore *Store, key string, ws *WorktreeState) (bool, error) {
	if err := stateStore.SetArchivedFull(key, ws); err != nil {
		return false, fmt.Errorf("set archived state: %w", err)
	}

	clean := true

	if ws.WorktreePath != "" {
		removeCmd := command.GitWorktreeRemove(ws.WorktreePath, true)
		result, err := executor.Execute([]command.Command{removeCmd})
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to execute worktree remove: %v\n", err)
			clean = false
		} else if len(result.Results) > 0 && result.Results[0].Error != nil {
			errMsg := result.Results[0].Error.Error()
			output := result.Results[0].Output
			combined := errMsg + " " + output
			if !strings.Contains(combined, "not a valid working tree") &&
				!strings.Contains(combined, "is not a working tree") {
				_, _ = fmt.Fprintf(os.Stderr, "warning: worktree remove failed: %s\n", combined)
				clean = false
			}
		}
	}

	if ws.Branch != "" {
		deleteCmd := command.GitBranchForceDelete(ws.Branch)
		result, err := executor.Execute([]command.Command{deleteCmd})
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to execute branch delete: %v\n", err)
			clean = false
		} else if len(result.Results) > 0 && result.Results[0].Error != nil {
			errMsg := result.Results[0].Error.Error()
			output := result.Results[0].Output
			combined := errMsg + " " + output
			if !strings.Contains(combined, "not found") {
				_, _ = fmt.Fprintf(os.Stderr, "warning: branch delete failed: %s\n", combined)
				clean = false
			}
		}
	}

	return clean, nil
}
