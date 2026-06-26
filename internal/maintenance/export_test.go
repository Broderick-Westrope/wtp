package maintenance

import (
	"context"
	"time"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/github"
)

// Test helpers — expose setters/restorers for package-level vars.

var (
	origIsGHAvailable   = isGHAvailable
	origGetPRForBranch  = getPRForBranch
	origNewExecutor     = newExecutor
	origIsWorktreeDirty = isWorktreeDirty
	origTimeNow         = timeNow
)

func SetIsGHAvailable(fn func() bool) { isGHAvailable = fn }
func RestoreIsGHAvailable()           { isGHAvailable = origIsGHAvailable }

func SetGetPRForBranch(fn func(ctx context.Context, branch string) (*github.PRInfo, error)) {
	getPRForBranch = fn
}
func RestoreGetPRForBranch() { getPRForBranch = origGetPRForBranch }

func SetNewExecutor(fn func() command.Executor) { newExecutor = fn }
func RestoreNewExecutor()                       { newExecutor = origNewExecutor }

func SetIsWorktreeDirty(fn func(mainRepoPath, worktreePath string) (bool, error)) {
	isWorktreeDirty = fn
}
func RestoreIsWorktreeDirty() { isWorktreeDirty = origIsWorktreeDirty }

func SetTimeNow(fn func() time.Time) { timeNow = fn }
func RestoreTimeNow()                { timeNow = origTimeNow }
