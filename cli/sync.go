package cli

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/cache"
	"github.com/Broderick-Westrope/wtp/v3/internal/config"
	"github.com/Broderick-Westrope/wtp/v3/internal/launchd"
	"github.com/Broderick-Westrope/wtp/v3/internal/maintenance"
	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

const (
	syncCommandName   = "sync"
	scheduledFlagName = "scheduled"
	backgroundFlag    = "background"

	// maxSyncLogBytes caps the scheduled agent's log; past it the log is
	// truncated at the start of the next run.
	maxSyncLogBytes = 1 << 20
)

// envPassedToAgent lists the variables baked into the launchd plist. PATH lets
// the job find git, gh and lsof; the XDG and gh overrides keep it reading the
// same state, cache and gh config as the shell that installed it. Tokens are
// deliberately excluded: gh resolves credentials from its own config per run.
var envPassedToAgent = []string{
	"PATH",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
	"GH_CONFIG_DIR", "GH_HOST",
}

// Variables to allow mocking in tests.
var (
	syncGOOS                            = runtime.GOOS
	syncLaunchctl     launchd.Launchctl = launchd.ExecLaunchctl{}
	syncPlistPath                       = launchd.PlistPath
	syncStorageRoot                     = xdg.WorktreeStorageRoot
	syncDiscoverRepos                   = maintenance.DiscoverRepos
	syncNewRunner                       = func(main string, id *remote.RepoIdentifier, warn io.Writer) repoSyncer {
		return maintenance.NewRunner(state.NewStore(), cache.NewStore(), id, main, warn)
	}
)

type repoSyncer interface {
	Sync(ctx context.Context) (maintenance.Result, error)
}

func newSyncCommand() *cli.Command {
	return &cli.Command{
		Name:  syncCommandName,
		Usage: "Check PRs on GitHub, auto-archive merged worktrees and refresh PR/CI status",
		UsageText: "wtp sync [--all]\n" +
			"   wtp sync --install | --uninstall",
		Description: "Checks the pull request of every worktree in the current repository (or every " +
			"repository with wtp worktrees, with --all). Worktrees whose PR was merged or closed are " +
			"archived unless they have uncommitted changes or a process is using them; the PR/CI status " +
			"shown by 'wtp list' is refreshed for the rest.\n\n" +
			"No other wtp command contacts GitHub. Install the background agent to keep everything " +
			"current without running this by hand:\n\n" +
			"  wtp sync --install    # macOS: launchd agent, syncs all repos every maintenance_interval (1h)\n" +
			"  wtp sync --uninstall",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Aliases: []string{"a"}, Usage: "Sync every repository that has wtp worktrees"},
			&cli.BoolFlag{Name: "install", Usage: "Install the scheduled background sync agent (macOS)"},
			&cli.BoolFlag{Name: "uninstall", Usage: "Remove the scheduled background sync agent"},
			&cli.BoolFlag{
				Name:   scheduledFlagName,
				Usage:  "Sync all repositories if due, queueing notices (used by the scheduled agent)",
				Hidden: true,
			},
			&cli.BoolFlag{
				Name:   backgroundFlag,
				Usage:  "Queue auto-archive notices instead of printing them (used by background refreshes)",
				Hidden: true,
			},
		},
		Action: syncCommand,
	}
}

// isBackgroundSync reports whether args invoke a sync that runs detached
// from the user's terminal.
func isBackgroundSync(args []string) bool {
	return slices.Contains(args, "--"+scheduledFlagName) || slices.Contains(args, "--"+backgroundFlag)
}

type syncOptions struct {
	all        bool
	ifDue      bool
	background bool
}

func syncCommand(ctx context.Context, cmd *cli.Command) error {
	w := stdoutFor(ctx, cmd)
	switch {
	case cmd.Bool("install") && cmd.Bool("uninstall"):
		return stderrors.New("--install and --uninstall are mutually exclusive")
	case cmd.Bool("install"):
		return installSyncAgent(ctx, w)
	case cmd.Bool("uninstall"):
		return uninstallSyncAgent(w)
	}

	scheduled := cmd.Bool(scheduledFlagName)
	return runSync(ctx, w, syncOptions{
		all:        cmd.Bool("all") || scheduled,
		ifDue:      scheduled,
		background: scheduled || cmd.Bool(backgroundFlag),
	})
}

// runSync syncs the selected repositories. Per-repository failures are
// reported and skipped so one broken repo cannot stall the rest; a run that
// finds another sync in progress exits successfully without doing anything.
func runSync(ctx context.Context, w io.Writer, opts syncOptions) error {
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	if opts.ifDue {
		trimSyncLog()
		if !maintenance.IsDue(cfg.MaintenanceInterval) {
			return nil
		}
	}

	unlock, err := maintenance.AcquireLock()
	if stderrors.Is(err, maintenance.ErrLocked) {
		if !opts.background {
			_, _ = fmt.Fprintln(w, "Another wtp sync is already running; skipped")
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()

	warn := stderrFor(ctx)
	_ = maintenance.Reap(state.NewStore(), cfg.ArchiveRetention, warn)

	repos, err := syncTargets(ctx, opts.all)
	if err != nil {
		return err
	}

	started := time.Now()
	totals, err := syncRepos(ctx, repos, warn)
	if err != nil {
		return err
	}

	if reportErr := reportArchived(w, totals.archived, opts.background); reportErr != nil {
		return reportErr
	}

	// Marked even when some repositories failed: their failures are usually
	// persistent (no origin, no access), and retrying them on every tick would
	// turn a cheap no-op tick into a full sync.
	if opts.all {
		_ = maintenance.MarkFullSync()
	}

	summary := fmt.Sprintf("Synced %d worktree(s) in %d repo(s): %d archived",
		totals.checked, len(repos), len(totals.archived))
	if totals.failed > 0 {
		summary += fmt.Sprintf(", %d failed", totals.failed)
	}
	if opts.background {
		summary = fmt.Sprintf("%s %s (%s)", time.Now().Format(time.RFC3339), summary,
			time.Since(started).Truncate(time.Millisecond))
	}
	_, err = fmt.Fprintln(w, summary)
	return err
}

type syncTotals struct {
	archived []maintenance.Archived
	checked  int
	failed   int
}

// syncRepos syncs each repository in turn. A repository that cannot be
// resolved or synced counts as one failure; only a missing gh CLI, which
// would fail every repository the same way, aborts the run.
func syncRepos(ctx context.Context, repos []string, warn io.Writer) (syncTotals, error) {
	var totals syncTotals
	for _, repoPath := range repos {
		mainRepoPath, repoID, err := resolveSyncRepo(ctx, repoPath)
		if err != nil {
			totals.failed++
			_, _ = fmt.Fprintf(warn, "warning: skipping %s: %v\n", repoPath, err)
			continue
		}

		result, err := syncNewRunner(mainRepoPath, repoID, warn).Sync(ctx)
		if stderrors.Is(err, maintenance.ErrGHUnavailable) {
			return syncTotals{}, fmt.Errorf("wtp sync needs the GitHub CLI: %w", err)
		}
		if err != nil {
			totals.failed++
			_, _ = fmt.Fprintf(warn, "warning: failed to sync %s: %v\n", repoID.StoragePath(), err)
			continue
		}
		totals.checked += result.Checked
		totals.failed += result.Failed
		totals.archived = append(totals.archived, result.Archived...)
	}
	return totals, nil
}

func syncTargets(ctx context.Context, all bool) ([]string, error) {
	if all {
		repos, err := syncDiscoverRepos(syncStorageRoot())
		if err != nil {
			return nil, fmt.Errorf("discovering repositories: %w", err)
		}
		return repos, nil
	}
	cwd, err := getwd(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := newRepository(ctx, cwd); err != nil {
		return nil, fmt.Errorf("not in a git repository (use --all to sync every repository): %w", err)
	}
	return []string{cwd}, nil
}

func reportArchived(w io.Writer, archived []maintenance.Archived, background bool) error {
	lines := make([]string, 0, len(archived))
	for _, a := range archived {
		lines = append(lines, a.String())
	}
	if background {
		// Logged for the record and queued so the next interactive command
		// tells the user, since nobody is watching this run.
		for _, line := range lines {
			_, _ = fmt.Fprintln(w, line)
		}
		return maintenance.QueueNotices(lines)
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func trimSyncLog() {
	if info, err := os.Stat(maintenance.LogPath()); err == nil && info.Size() > maxSyncLogBytes {
		_ = os.Truncate(maintenance.LogPath(), 0)
	}
}

func installSyncAgent(ctx context.Context, w io.Writer) error {
	if syncGOOS != "darwin" {
		return fmt.Errorf("--install uses launchd and is only supported on macOS; elsewhere schedule "+
			"'wtp sync --%s' with cron or a systemd timer every %d minutes", scheduledFlagName, launchd.DefaultTickMinutes)
	}

	env := procenv.From(ctx)
	program, err := agentProgram(env)
	if err != nil {
		return err
	}
	if dirErr := xdg.EnsureDir(maintenance.SyncDir()); dirErr != nil {
		return dirErr
	}

	var agentEnv []launchd.EnvVar
	for _, key := range envPassedToAgent {
		if value := env.Getenv(key); value != "" {
			agentEnv = append(agentEnv, launchd.EnvVar{Key: key, Value: value})
		}
	}

	plist, err := launchd.Render(program, maintenance.LogPath(), agentEnv, launchd.DefaultTickMinutes)
	if err != nil {
		return err
	}
	plistPath, err := syncPlistPath()
	if err != nil {
		return err
	}
	if installErr := launchd.Install(syncLaunchctl, plistPath, plist); installErr != nil {
		return fmt.Errorf("%w (the background agent is not running; retry 'wtp sync --install')", installErr)
	}

	cfg, _ := config.LoadGlobalConfig()
	_, err = fmt.Fprintf(w, "Installed background sync agent: %s\n"+
		"  Syncs every repository with wtp worktrees every %s (maintenance_interval)\n"+
		"  Log: %s\n", plistPath, cfg.MaintenanceInterval, maintenance.LogPath())
	return err
}

func uninstallSyncAgent(w io.Writer) error {
	plistPath, err := syncPlistPath()
	if err != nil {
		return err
	}
	if uninstallErr := launchd.Uninstall(syncLaunchctl, plistPath); uninstallErr != nil {
		return uninstallErr
	}
	_, err = fmt.Fprintln(w, "Removed background sync agent")
	return err
}

// agentProgram returns the argv prefix the agent should execute: the
// invocation's self prefix with its executable made absolute. That is the
// standalone wtp binary, or an embedding host followed by the arguments that
// reach its wtp. When the executable is also reachable through PATH under the
// same name (for example a Homebrew symlink), the PATH entry is preferred
// because it survives upgrades that move the real file.
func agentProgram(env *procenv.Env) ([]string, error) {
	if len(env.Self) == 0 || env.Self[0] == "" {
		return nil, stderrors.New("cannot determine how to re-run wtp for the background agent")
	}
	exe, err := resolveExecutable(env.Self[0], env.Getenv("PATH"))
	if err != nil {
		return nil, err
	}
	if onPath, lookErr := lookPathIn(filepath.Base(exe), env.Getenv("PATH")); lookErr == nil && sameFile(onPath, exe) {
		exe = onPath
	}
	return append([]string{exe}, env.Self[1:]...), nil
}

func resolveExecutable(name, pathEnv string) (string, error) {
	if !strings.ContainsRune(name, filepath.Separator) {
		found, err := lookPathIn(name, pathEnv)
		if err != nil {
			return "", fmt.Errorf("resolving %s for the background agent: %w", name, err)
		}
		return found, nil
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", fmt.Errorf("resolving %s for the background agent: %w", name, err)
	}
	return abs, nil
}

func lookPathIn(name, pathEnv string) (string, error) {
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func sameFile(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}
