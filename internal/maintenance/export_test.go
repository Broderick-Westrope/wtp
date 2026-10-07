package maintenance

import (
	"context"
	"time"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/github"
	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

// Test helpers — expose setters/restorers for package-level vars.

var (
	origIsGHAvailable   = isGHAvailable
	origGetPRForBranch  = getPRForBranch
	origGetCIStatus     = getCIStatus
	origNewExecutor     = newExecutor
	origIsWorktreeDirty = isWorktreeDirty
	origBusyPaths       = busyPaths
	origTimeNow         = timeNow
)

func SetIsGHAvailable(fn func() bool) { isGHAvailable = fn }
func RestoreIsGHAvailable()           { isGHAvailable = origIsGHAvailable }

func SetGetPRForBranch(fn func(ctx context.Context, branch string) (*github.PRInfo, error)) {
	getPRForBranch = func(ctx context.Context, _, branch string) (*github.PRInfo, error) {
		return fn(ctx, branch)
	}
}
func RestoreGetPRForBranch() { getPRForBranch = origGetPRForBranch }

func SetGetCIStatus(fn func(ctx context.Context, branch string) (*github.CIStatus, error)) {
	getCIStatus = func(ctx context.Context, _, branch string) (*github.CIStatus, error) {
		return fn(ctx, branch)
	}
}
func RestoreGetCIStatus() { getCIStatus = origGetCIStatus }

// SetNewExecutor replaces the executor factory; fn receives the environment
// Sync runs subprocesses in.
func SetNewExecutor(fn func(env *procenv.Env) command.Executor) { newExecutor = fn }
func RestoreNewExecutor()                                       { newExecutor = origNewExecutor }

func SetIsWorktreeDirty(fn func(mainRepoPath, worktreePath string) (bool, error)) {
	isWorktreeDirty = func(_ context.Context, mainRepoPath, worktreePath string) (bool, error) {
		return fn(mainRepoPath, worktreePath)
	}
}
func RestoreIsWorktreeDirty() { isWorktreeDirty = origIsWorktreeDirty }

func SetBusyPaths(paths []string) { busyPaths = func(context.Context) []string { return paths } }
func RestoreBusyPaths()           { busyPaths = origBusyPaths }

func SetTimeNow(fn func() time.Time) { timeNow = fn }
func RestoreTimeNow()                { timeNow = origTimeNow }

var ParseLsofNames = parseLsofNames
