package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/Broderick-Westrope/wtp/v3/internal/cache"
	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/config"
	"github.com/Broderick-Westrope/wtp/v3/internal/errors"
	"github.com/Broderick-Westrope/wtp/v3/internal/git"
	"github.com/Broderick-Westrope/wtp/v3/internal/github"
	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

// Display constants
const (
	headDisplayLength = 8
	ghHintFileName    = ".gh-hint-shown"
)

const (
	defaultMaxPathWidth = 56 // also used as default max branch column width
)

// worktreePRCI holds PR/CI display data for a single worktree, keyed by branch name.
type worktreePRCI struct {
	prFmt string
	ciFmt string
}

// Variables to allow mocking in tests
var (
	listGetwd        = getwd
	listNewGitRepo   = newRepository
	listGetRemoteURL = func(ctx context.Context, mainRepoPath string) (string, error) {
		repo, err := newRepository(ctx, mainRepoPath)
		if err != nil {
			return "", err
		}
		return repo.GetRemoteURL("origin")
	}
	listNewExecutor  = command.NewRealExecutor
	getTerminalWidth = func(ctx context.Context) int {
		width := 0
		if file, ok := procenv.From(ctx).Stdout.(*os.File); ok {
			width, _, _ = term.GetSize(int(file.Fd()))
		}
		if width <= 0 {
			return 80 //nolint:mnd // Default terminal width
		}
		return width
	}
	listIsGHAvailable       = github.IsAvailable
	listSpawnBackgroundSync = spawnBackgroundSync
)

// newListCommand creates the list command definition
func newListCommand() *cli.Command {
	return &cli.Command{
		Name:    "list",
		Aliases: []string{"ls"},
		Usage:   "List all worktrees",
		Description: "Shows all worktrees with their branches, PR/CI status, and HEAD commits.\n\n" +
			"PR/CI status comes from the local cache, so listing never waits on GitHub. When it is " +
			"older than cache_ttl, a background 'wtp sync' refreshes it for the next listing.",
		ShellComplete: completeList,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "compact",
				Aliases: []string{"c"},
				Usage:   "Minimize column widths for narrow or redirected output",
			},
			&cli.IntFlag{
				Name:    "max-branch-width",
				Aliases: []string{"max-path-width"}, // max-path-width kept as backward-compat alias
				Usage:   fmt.Sprintf("Maximum width for BRANCH column (default %d)", defaultMaxPathWidth),
				Value:   defaultMaxPathWidth,
			},
			&cli.BoolFlag{
				Name:    "quiet",
				Aliases: []string{"q"},
				Usage:   "Only display branch names",
			},
			&cli.BoolFlag{
				Name:  "all",
				Usage: "Show archived worktrees",
			},
			&cli.BoolFlag{
				Name:  "no-sync",
				Usage: "Don't start a background refresh of stale PR/CI status",
			},
		},
		Action: listCommand,
	}
}

func listCommand(ctx context.Context, cmd *cli.Command) error {
	cwd, err := listGetwd(ctx)
	if err != nil {
		return errors.DirectoryAccessFailed("access current", ".", err)
	}

	repo, err := listNewGitRepo(ctx, cwd)
	if err != nil {
		return errors.NotInGitRepository()
	}

	mainRepoPath, err := repo.GetMainWorktreePath()
	if err != nil {
		return errors.GitCommandFailed("get main worktree path", err.Error())
	}

	w := stdoutFor(ctx, cmd)

	opts := resolveListDisplayOptions(ctx, cmd, w)
	opts.Quiet = cmd.Bool("quiet")
	opts.ShowAll = cmd.Bool("all")
	opts.NoSync = cmd.Bool("no-sync")

	executor := listNewExecutor(procenv.From(ctx))
	return listCommandWithCommandExecutor(ctx, cmd, w, executor, mainRepoPath, opts)
}

func listCommandWithCommandExecutor( //nolint:gocyclo // orchestrates many distinct display paths
	ctx context.Context, _ *cli.Command, w io.Writer, executor command.Executor, mainRepoPath string,
	opts listDisplayOptions,
) error {
	cwd, err := listGetwd(ctx)
	if err != nil {
		return errors.DirectoryAccessFailed("access current", ".", err)
	}

	listCmd := command.GitWorktreeList()
	result, err := executor.Execute([]command.Command{listCmd})
	if err != nil {
		return errors.GitCommandFailed("git worktree list", err.Error())
	}

	worktrees := git.ParseWorktreeListOutput(result.Results[0].Output)

	if len(worktrees) == 0 {
		if !opts.Quiet {
			if _, err := fmt.Fprintln(w, "No worktrees found"); err != nil {
				return err
			}
		}
		return nil
	}

	// Try to get remote URL for state/cache key derivation
	var repoID *remote.RepoIdentifier
	if remoteURL, rerr := listGetRemoteURL(ctx, mainRepoPath); rerr == nil {
		if id, perr := remote.Parse(remoteURL); perr == nil {
			repoID = &id
		}
	}

	// Load state
	stateStore := state.NewStore()
	st, _ := stateStore.Load()

	// Build set of archived branches from state.json. Archived branches no
	// longer appear in `git worktree list`, so state is the source of truth.
	archivedBranches := make(map[string]bool)
	if repoID != nil {
		prefix := repoID.StoragePath() + "::"
		for key, ws := range st.Worktrees {
			if !ws.Archived || !strings.HasPrefix(key, prefix) {
				continue
			}
			if _, branch := remote.ParseStateKey(key); branch != "" {
				archivedBranches[branch] = true
			}
		}
	}

	// Filter out archived worktrees unless --all. Archived worktrees normally
	// aren't in git anymore, but partial archive failures can leave them behind.
	displayWorktrees := make([]git.Worktree, 0, len(worktrees))
	for _, wt := range worktrees {
		if !opts.ShowAll && archivedBranches[wt.Branch] {
			continue
		}
		displayWorktrees = append(displayWorktrees, wt)
	}

	// Synthesize rows for archived entries when --all is set — they no longer
	// exist in git, so their metadata comes from state.json.
	if opts.ShowAll && repoID != nil {
		displayWorktrees = appendArchivedEntries(displayWorktrees, st, repoID)
	}

	// PR/CI data collection
	prciData := make(map[string]worktreePRCI)
	ghAvailable := listIsGHAvailable()

	if ghAvailable && !opts.Quiet && repoID != nil {
		globalCfg, _ := config.LoadGlobalConfig()
		stale := loadPRCIFromCache(displayWorktrees, repoID, archivedBranches, prciData, globalCfg.CacheTTL)
		if stale && !opts.NoSync && !backgroundSyncDisabled(ctx) {
			listSpawnBackgroundSync(ctx)
		}
	} else if !ghAvailable && !opts.Quiet {
		maybeShowGHNotAvailableHint(stderrFor(ctx))
	}

	if opts.Quiet {
		return displayWorktreesQuiet(w, displayWorktrees)
	}

	termWidth := getTerminalWidth(ctx)
	if !opts.Compact && !opts.OutputIsTTY {
		// Redirected output gets the borderless compact format for easier parsing.
		opts.Compact = true
	}

	return displayWorktreesTable(w, displayWorktrees, cwd, ghAvailable, prciData, archivedBranches, termWidth, opts)
}

// prciFromCache builds a worktreePRCI entry from a cached record.
func prciFromCache(cached *cache.WorktreeCache) worktreePRCI {
	prFmt := github.FormatPRState(nil)
	if cached.PRNumber > 0 {
		prFmt = github.FormatPRState(&github.PRInfo{
			Number: cached.PRNumber,
			State:  cached.PRState,
		})
	}
	return worktreePRCI{
		prFmt: prFmt,
		ciFmt: cached.CIStatus,
	}
}

// loadPRCIFromCache fills prciData from the PR/CI cache without touching the
// network, and reports whether any displayed branch is missing from the cache
// or older than ttl. Archived branches are skipped — they no longer exist in
// git. The cache is kept current by `wtp sync`.
func loadPRCIFromCache(
	worktrees []git.Worktree,
	repoID *remote.RepoIdentifier,
	archivedBranches map[string]bool,
	prciData map[string]worktreePRCI,
	ttl time.Duration,
) bool {
	c, err := cache.NewStore().Load()
	if err != nil {
		return true
	}

	stale := false
	for _, wt := range worktrees {
		if wt.Branch == "" || wt.Branch == git.DetachedKeyword || wt.IsMain || archivedBranches[wt.Branch] {
			continue
		}
		entry, ok := c.Worktrees[repoID.StateKey(wt.Branch)]
		if !ok {
			stale = true
			continue
		}
		if time.Since(entry.UpdatedAt) > ttl {
			stale = true
		}
		prciData[wt.Branch] = prciFromCache(&entry)
	}
	return stale
}

// noBackgroundSyncEnv disables the background refresh `wtp list` starts when
// cached PR/CI status is stale, for scripts, offline use and tests that must
// not leave a detached process writing into their directories.
const noBackgroundSyncEnv = "WTP_NO_BACKGROUND_SYNC"

func backgroundSyncDisabled(ctx context.Context) bool {
	value := procenv.From(ctx).Getenv(noBackgroundSyncEnv)
	if value == "" {
		return false
	}
	disabled, err := strconv.ParseBool(value)
	return err != nil || disabled
}

// spawnBackgroundSync starts a detached `wtp sync --background` for the
// current repository so the next list shows fresh PR/CI status, without this
// one waiting on GitHub. The child takes the sync lock, so a refresh already
// in flight (or the scheduled agent) makes it exit immediately.
func spawnBackgroundSync(ctx context.Context) {
	env := procenv.From(ctx)
	if len(env.Self) == 0 || env.Dir == "" {
		return
	}
	args := append(slices.Clone(env.Self[1:]), syncCommandName, "--"+backgroundFlag)
	cmd := exec.Command(env.Self[0], args...)
	cmd.Dir = env.Dir
	cmd.Env = env.Environ
	cmd.SysProcAttr = detachedAttr()
	if err := cmd.Start(); err != nil {
		return
	}
	_ = cmd.Process.Release()
}

// appendArchivedEntries appends synthetic worktree rows for archived state
// entries belonging to the current repo. Entries whose branch already exists
// in the display list (partial archive failures) are skipped. The synthetic
// rows are sorted by branch name for deterministic output.
func appendArchivedEntries(
	displayWorktrees []git.Worktree,
	st state.State,
	repoID *remote.RepoIdentifier,
) []git.Worktree {
	existing := make(map[string]bool, len(displayWorktrees))
	for _, wt := range displayWorktrees {
		existing[wt.Branch] = true
	}

	prefix := repoID.StoragePath() + "::"

	var synthetic []git.Worktree
	for key, ws := range st.Worktrees {
		if !ws.Archived || !strings.HasPrefix(key, prefix) {
			continue
		}

		_, branch := remote.ParseStateKey(key)
		if branch == "" || existing[branch] {
			continue
		}

		synthetic = append(synthetic, git.Worktree{Branch: branch, HEAD: ws.CommitSHA})
	}

	sort.Slice(synthetic, func(i, j int) bool { return synthetic[i].Branch < synthetic[j].Branch })

	return append(displayWorktrees, synthetic...)
}

// completeList provides shell completion for the list command (flags only)
func completeList(ctx context.Context, cmd *cli.Command) {
	current, previous := completionArgsFromCommand(cmd)
	maybeCompleteFlagSuggestions(ctx, cmd, current, previous)
}

// formatBranchDisplay formats branch name for display in the BRANCH column.
func formatBranchDisplay(branch string) string {
	if branch == git.DetachedKeyword {
		return "(detached)"
	}
	if branch == "" {
		return "(no branch)"
	}
	return branch
}

// displayWorktreesQuiet outputs branch names only, one per line.
// Outputs "@" for the main worktree. Detached HEAD worktrees are omitted.
func displayWorktreesQuiet(w io.Writer, worktrees []git.Worktree) error {
	for _, wt := range worktrees {
		// Omit detached HEAD and empty branch worktrees from quiet output
		if wt.Branch == git.DetachedKeyword || wt.Branch == "" {
			continue
		}
		var name string
		if wt.IsMain {
			name = "@"
		} else {
			name = wt.Branch
		}
		if _, err := fmt.Fprintln(w, name); err != nil {
			return err
		}
	}
	return nil
}

// listRow holds display data for one row in the worktree table.
type listRow struct {
	branchDisplay string
	pr            string
	ci            string
	head          string
}

// displayWorktreesTable renders the worktree list as a lipgloss table.
func displayWorktreesTable(
	w io.Writer,
	worktrees []git.Worktree,
	currentPath string,
	ghAvailable bool,
	prciData map[string]worktreePRCI,
	archivedBranches map[string]bool,
	termWidth int,
	opts listDisplayOptions,
) error {
	if termWidth <= 0 {
		termWidth = 80
	}

	rows := buildListRows(worktrees, currentPath, ghAvailable, prciData, archivedBranches)
	if len(rows) == 0 {
		return nil
	}

	tbl := newListTable(opts)
	if ghAvailable {
		tbl.Headers("BRANCH", "PR", "CI", "HEAD")
	} else {
		tbl.Headers("BRANCH", "HEAD")
	}

	for _, row := range rows {
		head := row.head
		if len(head) > headDisplayLength {
			head = head[:headDisplayLength]
		}

		branch := truncateStr(row.branchDisplay, opts.MaxPathWidth)
		if ghAvailable {
			tbl.Row(branch, row.pr, row.ci, head)
		} else {
			tbl.Row(branch, head)
		}
	}

	rendered := tbl.Render()
	if lipgloss.Width(rendered) > termWidth {
		rendered = tbl.Width(termWidth).Render()
	}

	_, err := fmt.Fprintln(w, rendered)
	return err
}

// newListTable builds the base table for list output. Compact mode drops all
// borders for narrow or redirected output; otherwise a rounded border is used.
func newListTable(opts listDisplayOptions) *table.Table {
	tbl := table.New().Wrap(false)

	if opts.Compact {
		compactStyle := lipgloss.NewStyle().PaddingRight(2) //nolint:mnd // two-space column separator
		return tbl.
			BorderTop(false).
			BorderBottom(false).
			BorderLeft(false).
			BorderRight(false).
			BorderHeader(false).
			BorderColumn(false).
			StyleFunc(func(_, _ int) lipgloss.Style { return compactStyle })
	}

	cellStyle := lipgloss.NewStyle().Padding(0, 1)
	return tbl.
		Border(lipgloss.RoundedBorder()).
		BorderRow(false).
		StyleFunc(func(_, _ int) lipgloss.Style { return cellStyle })
}

// buildListRows constructs display rows for the worktree table.
func buildListRows(
	worktrees []git.Worktree,
	currentPath string,
	ghAvailable bool,
	prciData map[string]worktreePRCI,
	archivedBranches map[string]bool,
) []listRow {
	rows := make([]listRow, 0, len(worktrees))

	for _, wt := range worktrees {
		branchDisplay := formatBranchDisplay(wt.Branch)
		if wt.IsMain {
			branchDisplay = "@"
		}
		if wt.Path == currentPath {
			branchDisplay += "*"
		}
		if archivedBranches[wt.Branch] {
			branchDisplay += " (archived)"
		}

		var pr, ci string
		if ghAvailable && !wt.IsMain && wt.Branch != git.DetachedKeyword && wt.Branch != "" {
			if data, ok := prciData[wt.Branch]; ok {
				pr = data.prFmt
				ci = data.ciFmt
			}
		}

		rows = append(rows, listRow{
			branchDisplay: branchDisplay,
			pr:            pr,
			ci:            ci,
			head:          wt.HEAD,
		})
	}

	return rows
}

// truncateStr truncates a string to fit within maxWidth (in runes), using ellipsis.
func truncateStr(s string, maxWidth int) string {
	runes := []rune(s)
	if maxWidth <= 0 || len(runes) <= maxWidth {
		return s
	}

	const ellipsis = "..."
	ellipsisLen := len([]rune(ellipsis)) // 3
	if maxWidth <= ellipsisLen {
		return string(runes[:maxWidth])
	}

	availableWidth := maxWidth - ellipsisLen
	startLen := availableWidth / 3 //nolint:mnd // show 1/3 start, 2/3 end
	endLen := availableWidth - startLen

	return string(runes[:startLen]) + ellipsis + string(runes[len(runes)-endLen:])
}

// maybeShowGHNotAvailableHint shows a one-time hint about the gh CLI being unavailable.
func maybeShowGHNotAvailableHint(errW io.Writer) {
	hintFile := filepath.Join(xdg.WtpDataDir(), ghHintFileName)
	if _, err := os.Stat(hintFile); err == nil {
		return // already shown
	}
	_, _ = fmt.Fprintln(errW, "hint: install 'gh' CLI for PR/CI status in wtp list")
	_ = xdg.EnsureDir(xdg.WtpDataDir())
	_ = os.WriteFile(hintFile, []byte{}, 0o644) //nolint:gosec,mnd // hint file, world-readable is fine
}

type listDisplayOptions struct {
	Compact      bool
	MaxPathWidth int
	OutputIsTTY  bool
	Quiet        bool
	ShowAll      bool
	NoSync       bool
}

func resolveListDisplayOptions(ctx context.Context, cmd *cli.Command, w io.Writer) listDisplayOptions {
	maxPathWidth := cmd.Int("max-branch-width")
	if maxPathWidth == defaultMaxPathWidth && !cmd.IsSet("max-branch-width") && !cmd.IsSet("max-path-width") {
		if envValue := procenv.From(ctx).Getenv("WTP_LIST_MAX_PATH"); envValue != "" {
			if parsed, err := strconv.Atoi(envValue); err == nil && parsed > 0 {
				maxPathWidth = parsed
			}
		}
	}
	if maxPathWidth <= 0 {
		maxPathWidth = defaultMaxPathWidth
	}

	compact := cmd.Bool("compact")

	outputIsTTY := procenv.IsTerminal(w)

	return listDisplayOptions{
		Compact:      compact,
		MaxPathWidth: maxPathWidth,
		OutputIsTTY:  outputIsTTY,
	}
}
