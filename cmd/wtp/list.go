package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"

	"github.com/Broderick-Westrope/wtp/v3/internal/cache"
	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/config"
	"github.com/Broderick-Westrope/wtp/v3/internal/errors"
	"github.com/Broderick-Westrope/wtp/v3/internal/git"
	"github.com/Broderick-Westrope/wtp/v3/internal/github"
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
	listGetwd        = os.Getwd
	listNewGitRepo   = git.NewRepository
	listGetRemoteURL = func(mainRepoPath string) (string, error) {
		repo, err := git.NewRepository(mainRepoPath)
		if err != nil {
			return "", err
		}
		return repo.GetRemoteURL("origin")
	}
	listNewExecutor  = command.NewRealExecutor
	getTerminalWidth = func() int {
		width, _, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil || width <= 0 {
			return 80 //nolint:mnd // Default terminal width
		}
		return width
	}
	listIsGHAvailable  = github.IsAvailable
	listGetPRForBranch = github.GetPRForBranch
	listGetCIStatus    = github.GetCIStatus
)

// NewListCommand creates the list command definition
func NewListCommand() *cli.Command {
	return &cli.Command{
		Name:          "list",
		Aliases:       []string{"ls"},
		Usage:         "List all worktrees",
		Description:   "Shows all worktrees with their branches, PR/CI status, and HEAD commits.",
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
				Usage: "Skip gh calls and auto-archive",
			},
		},
		Action: listCommand,
	}
}

func listCommand(ctx context.Context, cmd *cli.Command) error {
	cwd, err := listGetwd()
	if err != nil {
		return errors.DirectoryAccessFailed("access current", ".", err)
	}

	repo, err := listNewGitRepo(cwd)
	if err != nil {
		return errors.NotInGitRepository()
	}

	mainRepoPath, err := repo.GetMainWorktreePath()
	if err != nil {
		return errors.GitCommandFailed("get main worktree path", err.Error())
	}

	w := cmd.Root().Writer
	if w == nil {
		w = os.Stdout
	}

	opts := resolveListDisplayOptions(cmd, w)
	opts.Quiet = cmd.Bool("quiet")
	opts.ShowAll = cmd.Bool("all")
	opts.NoSync = cmd.Bool("no-sync")

	executor := listNewExecutor()
	return listCommandWithCommandExecutor(ctx, cmd, w, executor, mainRepoPath, opts)
}

func listCommandWithCommandExecutor( //nolint:gocyclo // orchestrates many distinct display paths
	ctx context.Context, _ *cli.Command, w io.Writer, executor command.Executor, mainRepoPath string,
	opts listDisplayOptions,
) error {
	cwd, err := listGetwd()
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
	if remoteURL, rerr := listGetRemoteURL(mainRepoPath); rerr == nil {
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

	if ghAvailable && !opts.NoSync && !opts.Quiet && repoID != nil {
		if err := fetchPRCIData(ctx, displayWorktrees, repoID, archivedBranches, prciData); err != nil {
			return err
		}
	} else if !ghAvailable && !opts.Quiet {
		maybeShowGHNotAvailableHint()
	}

	if opts.Quiet {
		return displayWorktreesQuiet(w, displayWorktrees)
	}

	termWidth := getTerminalWidth()
	if !opts.Compact && !opts.OutputIsTTY {
		// Redirected output gets the borderless compact format for easier parsing.
		opts.Compact = true
	}

	return displayWorktreesTable(w, displayWorktrees, cwd, ghAvailable, prciData, archivedBranches, termWidth, opts)
}

// prciFromCache builds a worktreePRCI entry from a cached record.
func prciFromCache(cached *cache.WorktreeCache) worktreePRCI {
	prFmt := ""
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

// prciSharedState holds the shared mutable state passed to per-branch fetch goroutines.
type prciSharedState struct {
	mu         sync.Mutex
	errorCount atomic.Int32
	newEntries map[string]cache.WorktreeCache
	prciData   map[string]worktreePRCI
}

// fetchPRCIForBranch fetches PR/CI data for a single branch and updates shared state.
// Network calls are made outside the lock; only map writes are protected by mu.
func fetchPRCIForBranch(
	ctx context.Context,
	wt git.Worktree,
	key string,
	cacheStore *cache.Store,
	ttl time.Duration,
	shared *prciSharedState,
) error {
	// Use cached data if fresh
	if cached, ok := cacheStore.Get(key); ok && !cacheStore.IsExpired(&cached, ttl) {
		shared.mu.Lock()
		shared.prciData[wt.Branch] = prciFromCache(&cached)
		shared.mu.Unlock()
		return nil
	}

	// Fetch fresh data — network calls outside the lock.
	// CI checks require a PR, so skip the CI call when there's no PR to avoid
	// a wasted round-trip that always returns "no pull requests found".
	pr, prErr := listGetPRForBranch(ctx, wt.Branch)
	var ci *github.CIStatus
	var ciErr error
	if pr != nil {
		ci, ciErr = listGetCIStatus(ctx, wt.Branch)
	}
	if prErr != nil || ciErr != nil {
		shared.errorCount.Add(1)
	}

	prFmt := github.FormatPRState(pr)
	ciFmt := github.FormatCIStatus(ci)

	updateSharedWithFreshData(wt.Branch, key, pr, prErr, ciErr, prFmt, ciFmt, shared)

	return nil
}

// updateSharedWithFreshData writes freshly-fetched PR/CI data into shared state
// under the mutex.
func updateSharedWithFreshData(
	branch, key string,
	pr *github.PRInfo,
	prErr, ciErr error,
	prFmt, ciFmt string,
	shared *prciSharedState,
) {
	shared.mu.Lock()
	defer shared.mu.Unlock()

	// Only cache successful fetches — partial failures would poison the
	// cache with PRNumber=0/PRState="" and suppress fresh attempts until
	// the TTL expires.
	if prErr == nil && ciErr == nil {
		entry := cache.WorktreeCache{CIStatus: ciFmt}
		if pr != nil {
			entry.PRNumber = pr.Number
			entry.PRState = pr.State
			entry.PRTitle = pr.Title
		}
		shared.newEntries[key] = entry
	}

	shared.prciData[branch] = worktreePRCI{
		prFmt: prFmt,
		ciFmt: ciFmt,
	}
}

// fetchPRCIData fetches PR/CI info for non-main non-detached worktrees, updating prciData
// in-place. Fetches across branches are parallelized using errgroup.
// Archived branches are skipped — they no longer exist in git.
// Auto-archive is handled by the maintenance system, not list.
func fetchPRCIData(
	ctx context.Context,
	worktrees []git.Worktree,
	repoID *remote.RepoIdentifier,
	archivedBranches map[string]bool,
	prciData map[string]worktreePRCI,
) error {
	cacheStore := cache.NewStore()
	globalCfg, _ := config.LoadGlobalConfig()
	ttl := globalCfg.CacheTTL

	shared := &prciSharedState{
		newEntries: make(map[string]cache.WorktreeCache),
		prciData:   prciData,
	}

	g, gCtx := errgroup.WithContext(ctx)

	for _, wt := range worktrees {
		if wt.Branch == "" || wt.Branch == git.DetachedKeyword || wt.IsMain || archivedBranches[wt.Branch] {
			continue
		}

		key := repoID.StateKey(wt.Branch)
		g.Go(func() error {
			return fetchPRCIForBranch(gCtx, wt, key, cacheStore, ttl, shared)
		})
	}

	if err := g.Wait(); err != nil {
		return err
	}

	if count := shared.errorCount.Load(); count > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "warning: failed to fetch PR/CI status for %d branch(es)\n", count)
	}

	_ = cacheStore.SetBatch(shared.newEntries)
	return nil
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
func completeList(_ context.Context, cmd *cli.Command) {
	current, previous := completionArgsFromCommand(cmd)
	maybeCompleteFlagSuggestions(cmd, current, previous)
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
func maybeShowGHNotAvailableHint() {
	hintFile := filepath.Join(xdg.WtpDataDir(), ghHintFileName)
	if _, err := os.Stat(hintFile); err == nil {
		return // already shown
	}
	_, _ = fmt.Fprintln(os.Stderr, "hint: install 'gh' CLI for PR/CI status in wtp list")
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

func resolveListDisplayOptions(cmd *cli.Command, w io.Writer) listDisplayOptions {
	maxPathWidth := cmd.Int("max-branch-width")
	if maxPathWidth == defaultMaxPathWidth && !cmd.IsSet("max-branch-width") && !cmd.IsSet("max-path-width") {
		if envValue := os.Getenv("WTP_LIST_MAX_PATH"); envValue != "" {
			if parsed, err := strconv.Atoi(envValue); err == nil && parsed > 0 {
				maxPathWidth = parsed
			}
		}
	}
	if maxPathWidth <= 0 {
		maxPathWidth = defaultMaxPathWidth
	}

	compact := cmd.Bool("compact")

	outputIsTTY := false
	if file, ok := w.(*os.File); ok {
		outputIsTTY = term.IsTerminal(int(file.Fd()))
	}

	return listDisplayOptions{
		Compact:      compact,
		MaxPathWidth: maxPathWidth,
		OutputIsTTY:  outputIsTTY,
	}
}
