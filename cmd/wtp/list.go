package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/urfave/cli/v3"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"

	"github.com/satococoa/wtp/v3/internal/cache"
	"github.com/satococoa/wtp/v3/internal/command"
	"github.com/satococoa/wtp/v3/internal/config"
	"github.com/satococoa/wtp/v3/internal/errors"
	"github.com/satococoa/wtp/v3/internal/git"
	"github.com/satococoa/wtp/v3/internal/github"
	"github.com/satococoa/wtp/v3/internal/remote"
	"github.com/satococoa/wtp/v3/internal/state"
	"github.com/satococoa/wtp/v3/internal/xdg"
)

// Display constants
const (
	branchHeaderDashes = 6
	headDisplayLength  = 8
	detachedKeyword    = "detached"
	ghHintFileName     = ".gh-hint-shown"
)

const (
	defaultMaxPathWidth = 56 // also used as default max branch column width
	superWideThreshold  = 160
	columnSep           = 2 // spaces between columns
	minBranchWidth      = 6 // minimum branch column width = len("BRANCH")
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

	worktrees := parseWorktreesFromOutput(result.Results[0].Output)

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

	// Build set of archived branches
	archivedBranches := make(map[string]bool)
	if repoID != nil {
		for _, wt := range worktrees {
			if wt.Branch != "" && !wt.IsMain {
				key := repoID.StateKey(wt.Branch)
				if st.Worktrees[key].Archived {
					archivedBranches[wt.Branch] = true
				}
			}
		}
	}

	// Filter out archived worktrees unless --all
	displayWorktrees := make([]git.Worktree, 0, len(worktrees))
	for _, wt := range worktrees {
		if !opts.ShowAll && archivedBranches[wt.Branch] {
			continue
		}
		displayWorktrees = append(displayWorktrees, wt)
	}

	// PR/CI data collection
	prciData := make(map[string]worktreePRCI)
	ghAvailable := listIsGHAvailable()

	if ghAvailable && !opts.NoSync && !opts.Quiet && repoID != nil {
		if err := fetchPRCIData(ctx, displayWorktrees, repoID, stateStore, archivedBranches, prciData); err != nil {
			return err
		}

		// Rebuild displayWorktrees after auto-archiving (unless --all)
		if !opts.ShowAll {
			remaining := make([]git.Worktree, 0, len(displayWorktrees))
			for _, wt := range displayWorktrees {
				if !archivedBranches[wt.Branch] {
					remaining = append(remaining, wt)
				}
			}
			displayWorktrees = remaining
		}
	} else if !ghAvailable && !opts.Quiet {
		maybeShowGHNotAvailableHint()
	}

	if opts.Quiet {
		return displayWorktreesQuiet(w, displayWorktrees)
	}

	termWidth := getTerminalWidth()
	if !opts.Compact {
		if !opts.OutputIsTTY {
			opts.Compact = true
		} else if termWidth >= superWideThreshold {
			// On super-wide terminals (>=160 cols), enable compact mode to prevent
			// comically wide BRANCH columns that waste horizontal space.
			opts.Compact = true
		}
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
	mu               sync.Mutex
	errorCount       atomic.Int32
	newEntries       map[string]cache.WorktreeCache
	archivedBranches map[string]bool
	prciData         map[string]worktreePRCI
}

// archiveRequest records a branch that needs to be archived after releasing the mutex.
type archiveRequest struct {
	branch   string
	prNumber int
}

// fetchPRCIForBranch fetches PR/CI data for a single branch and updates shared state.
// Network calls and archive I/O are made outside the lock; only map writes are protected by mu.
func fetchPRCIForBranch(
	ctx context.Context,
	wt git.Worktree,
	key string,
	repoID *remote.RepoIdentifier,
	stateStore *state.Store,
	cacheStore *cache.Store,
	ttl time.Duration,
	shared *prciSharedState,
) error {
	// Use cached data if fresh
	if cached, ok := cacheStore.Get(key); ok && !cacheStore.IsExpired(&cached, ttl) {
		var toArchive *archiveRequest

		shared.mu.Lock()
		shared.prciData[wt.Branch] = prciFromCache(&cached)
		if cached.PRState == github.StateMerged && !shared.archivedBranches[wt.Branch] {
			shared.archivedBranches[wt.Branch] = true
			toArchive = &archiveRequest{branch: wt.Branch, prNumber: cached.PRNumber}
		}
		shared.mu.Unlock()

		if toArchive != nil {
			autoArchiveBranch(toArchive.branch, toArchive.prNumber, repoID, stateStore)
		}
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
		shared.errorCount.Add(1) // count branches (not individual calls) that had fetch errors
	}

	prFmt := github.FormatPRState(pr)
	ciFmt := github.FormatCIStatus(ci)

	var toArchive *archiveRequest

	shared.mu.Lock()

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

	shared.prciData[wt.Branch] = worktreePRCI{
		prFmt: prFmt,
		ciFmt: ciFmt,
	}

	if pr != nil && pr.State == github.StateMerged && !shared.archivedBranches[wt.Branch] {
		shared.archivedBranches[wt.Branch] = true
		toArchive = &archiveRequest{branch: wt.Branch, prNumber: pr.Number}
	}

	shared.mu.Unlock()

	if toArchive != nil {
		autoArchiveBranch(toArchive.branch, toArchive.prNumber, repoID, stateStore)
	}

	return nil
}

// fetchPRCIData fetches PR/CI info for non-main non-detached worktrees, updating prciData and
// archivedBranches in-place. Auto-archive notices are printed to stderr.
// Fetches across branches are parallelized using errgroup.
func fetchPRCIData(
	ctx context.Context,
	worktrees []git.Worktree,
	repoID *remote.RepoIdentifier,
	stateStore *state.Store,
	archivedBranches map[string]bool,
	prciData map[string]worktreePRCI,
) error {
	cacheStore := cache.NewStore()
	globalCfg, _ := config.LoadGlobalConfig()
	ttl := globalCfg.CacheTTL

	shared := &prciSharedState{
		newEntries:       make(map[string]cache.WorktreeCache),
		archivedBranches: archivedBranches,
		prciData:         prciData,
	}

	g, gCtx := errgroup.WithContext(ctx)

	for _, wt := range worktrees {
		if wt.Branch == "" || wt.Branch == detachedKeyword || wt.IsMain {
			continue
		}

		key := repoID.StateKey(wt.Branch)
		g.Go(func() error {
			return fetchPRCIForBranch(gCtx, wt, key, repoID, stateStore, cacheStore, ttl, shared)
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

// autoArchiveBranch persists the archived flag and prints a notice to stderr.
// The caller is responsible for setting archivedBranches[branch] = true under the mutex
// before calling this function, so no shared map mutation happens here.
func autoArchiveBranch(
	branch string,
	prNumber int,
	repoID *remote.RepoIdentifier,
	stateStore *state.Store,
) {
	key := repoID.StateKey(branch)
	_ = stateStore.SetArchived(key, true)
	_, _ = fmt.Fprintf(os.Stderr, "Auto-archived %s (PR #%d merged)\n", branch, prNumber)
}

// completeList provides shell completion for the list command (flags only)
func completeList(_ context.Context, cmd *cli.Command) {
	current, previous := completionArgsFromCommand(cmd)
	maybeCompleteFlagSuggestions(cmd, current, previous)
}

func parseWorktreesFromOutput(output string) []git.Worktree {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var worktrees []git.Worktree
	var currentWorktree git.Worktree
	isFirst := true

	for _, line := range lines {
		if line == "" {
			if currentWorktree.Path != "" {
				if isFirst {
					currentWorktree.IsMain = true
					isFirst = false
				}
				worktrees = append(worktrees, currentWorktree)
				currentWorktree = git.Worktree{}
			}
			continue
		}

		if strings.HasPrefix(line, "worktree ") {
			currentWorktree.Path = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "HEAD ") {
			currentWorktree.HEAD = strings.TrimPrefix(line, "HEAD ")
		} else if strings.HasPrefix(line, "branch ") {
			currentWorktree.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		} else if line == detachedKeyword {
			currentWorktree.Branch = detachedKeyword
		}
	}

	if currentWorktree.Path != "" {
		if isFirst {
			currentWorktree.IsMain = true
		}
		worktrees = append(worktrees, currentWorktree)
	}

	return worktrees
}

// formatBranchDisplay formats branch name for display in the BRANCH column.
func formatBranchDisplay(branch string) string {
	if branch == detachedKeyword {
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
		if wt.Branch == detachedKeyword || wt.Branch == "" {
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

// displayWorktreesTable renders the tabular worktree list.
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

	rows, maxBranch, maxPR, maxCI := buildListRows(worktrees, currentPath, ghAvailable, prciData, archivedBranches)
	if len(rows) == 0 {
		return nil
	}

	bw, prw, ciw := computeListColumns(maxBranch, maxPR, maxCI, termWidth, ghAvailable, opts)

	// Print header
	if ghAvailable {
		if _, err := fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n", bw, "BRANCH", prw, "PR", ciw, "CI", "HEAD"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n",
			bw, strings.Repeat("-", branchHeaderDashes),
			prw, "--",
			ciw, "--",
			"----"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "%-*s  %s\n", bw, "BRANCH", "HEAD"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%-*s  %s\n", bw, strings.Repeat("-", branchHeaderDashes), "----"); err != nil {
			return err
		}
	}

	for _, row := range rows {
		head := row.head
		if len(head) > headDisplayLength {
			head = head[:headDisplayLength]
		}

		branch := truncateStr(row.branchDisplay, bw)
		if ghAvailable {
			pr := truncateStr(row.pr, prw)
			ci := truncateStr(row.ci, ciw)
			if _, err := fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n", bw, branch, prw, pr, ciw, ci, head); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(w, "%-*s  %s\n", bw, branch, head); err != nil {
				return err
			}
		}
	}

	return nil
}

// buildListRows constructs display rows and measures max column widths.
func buildListRows(
	worktrees []git.Worktree,
	currentPath string,
	ghAvailable bool,
	prciData map[string]worktreePRCI,
	archivedBranches map[string]bool,
) (rows []listRow, maxBranch, maxPR, maxCI int) {
	maxBranch = len("BRANCH")
	maxPR = len("PR")
	maxCI = len("CI")

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
		if ghAvailable && !wt.IsMain && wt.Branch != detachedKeyword && wt.Branch != "" {
			if data, ok := prciData[wt.Branch]; ok {
				pr = data.prFmt
				ci = data.ciFmt
			}
		}

		row := listRow{
			branchDisplay: branchDisplay,
			pr:            pr,
			ci:            ci,
			head:          wt.HEAD,
		}
		rows = append(rows, row)

		if len(branchDisplay) > maxBranch {
			maxBranch = len(branchDisplay)
		}
		if len(pr) > maxPR {
			maxPR = len(pr)
		}
		if len(ci) > maxCI {
			maxCI = len(ci)
		}
	}

	return rows, maxBranch, maxPR, maxCI
}

// computeListColumns determines column widths.
func computeListColumns(
	maxBranch, maxPR, maxCI, termWidth int,
	ghAvailable bool,
	opts listDisplayOptions,
) (bw, prw, ciw int) {
	prw = 0
	ciw = 0
	if ghAvailable {
		prw = maxPR
		ciw = maxCI
	}

	// Available width for branch column
	available := termWidth - columnSep - headDisplayLength
	if ghAvailable {
		available -= columnSep + prw + columnSep + ciw
	}

	bw = maxBranch
	if bw > available {
		bw = available
	}
	if bw < minBranchWidth {
		bw = minBranchWidth
	}

	if opts.MaxPathWidth > 0 {
		bw = min(bw, opts.MaxPathWidth)
		if bw < minBranchWidth {
			bw = minBranchWidth
		}
	}

	if opts.Compact {
		bw = min(bw, maxBranch)
		if bw < minBranchWidth {
			bw = minBranchWidth
		}
	}

	return bw, prw, ciw
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
