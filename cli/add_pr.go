package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/config"
	"github.com/Broderick-Westrope/wtp/v3/internal/errors"
	"github.com/Broderick-Westrope/wtp/v3/internal/git"
	"github.com/Broderick-Westrope/wtp/v3/internal/github"
	"github.com/Broderick-Westrope/wtp/v3/internal/maintenance"
	"github.com/Broderick-Westrope/wtp/v3/internal/marker"
	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

var (
	addIsGHAvailable = github.IsAvailable
	addGetPR         = github.GetPR
	addDiscoverRepos = maintenance.DiscoverRepos
)

const (
	shortSHALen = 8
	// defaultRemote is the remote wtp derives worktree storage paths from.
	defaultRemote = "origin"
)

// prCheckout is a pull request resolved to the local repository and remote
// it will be checked out from.
type prCheckout struct {
	pr           *github.PRInfo
	repo         *git.Repository
	cfg          *config.Config
	mainRepoPath string
	// remoteName is the remote whose URL matches the PR's base repository.
	remoteName string
	// branch is the local branch name: the head branch for same-repo PRs and
	// pr/<number> for forks, whose branch names can collide with local ones.
	branch string
}

func addPRCommand(ctx context.Context, cmd *cli.Command, w, errW io.Writer) error {
	if cmd.Args().Len() > 0 || cmd.String("branch") != "" {
		return fmt.Errorf("--pr cannot be combined with a branch argument or --branch")
	}

	ref, err := github.ParsePRRef(cmd.String("pr"))
	if err != nil {
		return err
	}

	if !addIsGHAvailable() {
		return fmt.Errorf("--pr needs the GitHub CLI (gh); install it from https://cli.github.com")
	}

	checkout, err := resolvePRCheckout(ctx, ref)
	if err != nil {
		return err
	}

	cmdExec := command.NewRealExecutor(procenv.From(ctx))
	return checkoutPR(ctx, cmd, w, errW, cmdExec, checkout)
}

// resolvePRCheckout looks the PR up on GitHub and finds the local clone of its
// base repository: the current repository when it has a matching remote,
// otherwise any repository with a wtp worktree. A bare PR number is always
// resolved by gh against the current repository.
func resolvePRCheckout(ctx context.Context, ref github.PRRef) (*prCheckout, error) {
	cwd, err := getwd(ctx)
	if err != nil {
		return nil, errors.DirectoryAccessFailed("access current", ".", err)
	}

	var cwdMain string
	if cwdRepo, repoErr := newRepository(ctx, cwd); repoErr == nil {
		cwdMain, err = cwdRepo.GetMainWorktreePath()
		if err != nil {
			cwdMain = cwdRepo.Path()
		}
	} else if ref.Owner == "" {
		return nil, errors.NotInGitRepository()
	}

	pr, err := addGetPR(ctx, cwd, ref.Arg())
	if err != nil {
		return nil, fmt.Errorf("look up pull request %s: %w", ref.Arg(), err)
	}

	base, err := github.ParsePRRef(pr.URL)
	if err != nil || base.Owner == "" {
		return nil, fmt.Errorf("gh returned an unexpected URL %q for pull request %s", pr.URL, ref.Arg())
	}

	mainRepoPath, remoteName, err := findPRRepo(ctx, cwdMain, base)
	if err != nil {
		return nil, err
	}

	repo, cfg, mainRepoPath, err := loadRepoAndConfig(ctx, mainRepoPath)
	if err != nil {
		return nil, err
	}

	branch := pr.HeadBranch
	if pr.IsCrossRepository {
		branch = fmt.Sprintf("pr/%d", pr.Number)
	}

	return &prCheckout{
		pr:           pr,
		repo:         repo,
		cfg:          cfg,
		mainRepoPath: mainRepoPath,
		remoteName:   remoteName,
		branch:       branch,
	}, nil
}

func findPRRepo(ctx context.Context, cwdMain string, base github.PRRef) (mainRepoPath, remoteName string, err error) {
	if cwdMain != "" {
		if name, ok := matchingRemote(ctx, cwdMain, base); ok {
			return cwdMain, name, nil
		}
	}

	repos, err := addDiscoverRepos(xdg.WorktreeStorageRoot())
	if err != nil {
		return "", "", fmt.Errorf("search for a local clone of %s/%s: %w", base.Owner, base.Repo, err)
	}
	for _, repoPath := range repos {
		if repoPath == cwdMain {
			continue
		}
		if name, ok := matchingRemote(ctx, repoPath, base); ok {
			return repoPath, name, nil
		}
	}

	// gh found the PR from the current repository's remotes, so the PR belongs
	// here even if no remote URL parses to the same owner/repo.
	if cwdMain != "" {
		return cwdMain, defaultRemote, nil
	}

	return "", "", fmt.Errorf(
		"no local clone of %s/%s found in the current repository or any repository with a wtp worktree; "+
			"run 'wtp add --pr' from inside your clone", base.Owner, base.Repo)
}

// matchingRemote returns the first remote of the repository at repoPath whose
// URL points at base's owner/repo. Hosts are not compared, since SSH host
// aliases such as github.com-work are common.
func matchingRemote(ctx context.Context, repoPath string, base github.PRRef) (string, bool) {
	repo, err := newRepository(ctx, repoPath)
	if err != nil {
		return "", false
	}
	remotes, err := repo.Remotes()
	if err != nil {
		return "", false
	}
	for _, r := range remotes {
		id, parseErr := remote.Parse(r.URL)
		if parseErr != nil {
			continue
		}
		if strings.EqualFold(id.Owner, base.Owner) && strings.EqualFold(id.Repo, base.Repo) {
			return r.Name, true
		}
	}
	return "", false
}

func checkoutPR(
	ctx context.Context,
	cmd *cli.Command,
	w, errW io.Writer,
	cmdExec command.Executor,
	c *prCheckout,
) error {
	pr := c.pr
	if _, err := fmt.Fprintf(w, "🔀 PR #%d: %s\n", pr.Number, pr.Title); err != nil {
		return err
	}
	if pr.State == github.StateMerged || pr.State == github.StateClosed {
		if _, err := fmt.Fprintf(w, "Note: PR #%d is %s.\n", pr.Number, strings.ToLower(pr.State)); err != nil {
			return err
		}
	}

	worktrees, err := c.repo.GetWorktrees()
	if err != nil {
		return err
	}
	for i := range worktrees {
		if worktrees[i].Branch == c.branch {
			return reusePRWorktree(ctx, cmd, w, errW, cmdExec, c, &worktrees[i])
		}
	}

	workTreePath, _, err := resolveWorktreePath(c.repo.GetRemoteURL, c.branch, cmd)
	if err != nil {
		return err
	}

	commitish, opts, err := preparePRBranch(w, c)
	if err != nil {
		return err
	}

	if dirErr := xdg.EnsureDir(filepath.Dir(workTreePath)); dirErr != nil {
		return dirErr
	}

	addCmd := command.GitWorktreeAdd(workTreePath, commitish, opts)
	addCmd.WorkDir = c.mainRepoPath
	result, err := cmdExec.Execute([]command.Command{addCmd})
	if err != nil {
		return err
	}
	if len(result.Results) > 0 && result.Results[0].Error != nil {
		return analyzeGitWorktreeError(workTreePath, c.branch, result.Results[0].Error, result.Results[0].Output)
	}

	return finishAdd(ctx, cmd, w, errW, cmdExec, c.cfg, c.mainRepoPath, workTreePath, c.branch)
}

// preparePRBranch fetches the PR head and returns the commitish and options
// for `git worktree add`. An existing local branch is used as it is, since it
// may hold the reviewer's own commits.
func preparePRBranch(w io.Writer, c *prCheckout) (string, command.GitWorktreeAddOptions, error) {
	exists, err := c.repo.BranchExists(c.branch)
	if err != nil {
		return "", command.GitWorktreeAddOptions{}, err
	}

	if !c.pr.IsCrossRepository {
		fetchErr := fetchPRBranch(w, c)
		if fetchErr != nil && !exists {
			return "", command.GitWorktreeAddOptions{}, fetchErr
		}
		if exists {
			if fetchErr != nil {
				if err := writef(w, "Warning: %v\n", fetchErr); err != nil {
					return "", command.GitWorktreeAddOptions{}, err
				}
			}
			return c.branch, command.GitWorktreeAddOptions{}, noteIfNotAtHead(w, c, c.branch)
		}
		upstream := c.remoteName + "/" + c.branch
		return upstream, command.GitWorktreeAddOptions{Branch: c.branch, Track: upstream}, nil
	}

	if exists {
		return c.branch, command.GitWorktreeAddOptions{}, noteIfNotAtHead(w, c, c.branch)
	}

	if err := writef(w, "Fetching PR #%d from %s into %s...\n", c.pr.Number, c.remoteName, c.branch); err != nil {
		return "", command.GitWorktreeAddOptions{}, err
	}
	refspec := fmt.Sprintf("+refs/pull/%d/head:refs/heads/%s", c.pr.Number, c.branch)
	if err := c.repo.ExecuteGitCommand("fetch", c.remoteName, refspec); err != nil {
		return "", command.GitWorktreeAddOptions{}, err
	}
	return c.branch, command.GitWorktreeAddOptions{}, nil
}

// fetchPRBranch updates the remote-tracking ref for a same-repo PR's head
// branch, first adding the branch to the remote's fetch refspecs when they
// don't cover it (as in single-branch clones), so tracking can be set up and
// later fetches keep it current.
func fetchPRBranch(w io.Writer, c *prCheckout) error {
	if err := writef(w, "Fetching %s from %s...\n", c.branch, c.remoteName); err != nil {
		return err
	}

	refspecs, err := c.repo.FetchRefspecs(c.remoteName)
	if err != nil {
		return err
	}
	if !refspecsCoverBranch(refspecs, c.remoteName, c.branch) {
		if err := c.repo.ExecuteGitCommand("remote", "set-branches", "--add", c.remoteName, c.branch); err != nil {
			return err
		}
	}

	refspec := fmt.Sprintf("+refs/heads/%s:refs/remotes/%s/%s", c.branch, c.remoteName, c.branch)
	return c.repo.ExecuteGitCommand("fetch", c.remoteName, refspec)
}

func refspecsCoverBranch(refspecs []string, remoteName, branch string) bool {
	wildcard := fmt.Sprintf("refs/heads/*:refs/remotes/%s/*", remoteName)
	exact := fmt.Sprintf("refs/heads/%s:refs/remotes/%s/%s", branch, remoteName, branch)
	for _, spec := range refspecs {
		spec = strings.TrimPrefix(spec, "+")
		if spec == wildcard || spec == exact {
			return true
		}
	}
	return false
}

// noteIfNotAtHead tells the user when a reused local branch isn't at the
// PR's head commit, without touching it.
func noteIfNotAtHead(w io.Writer, c *prCheckout, rev string) error {
	tip, err := c.repo.RevParse(rev)
	if err != nil || c.pr.HeadOID == "" || tip == c.pr.HeadOID {
		return nil //nolint:nilerr // the note is advisory; a failed lookup just skips it
	}

	pull := "git pull"
	if c.pr.IsCrossRepository {
		pull = fmt.Sprintf("git pull %s pull/%d/head", c.remoteName, c.pr.Number)
	}
	return writef(w, "Note: %s is at %s, not the PR head %s. Run '%s' in the worktree to update it.\n",
		c.branch, shortSHA(tip), shortSHA(c.pr.HeadOID), pull)
}

func reusePRWorktree(
	ctx context.Context,
	cmd *cli.Command,
	w, errW io.Writer,
	cmdExec command.Executor,
	c *prCheckout,
	wt *git.Worktree,
) error {
	if !c.pr.IsCrossRepository {
		if err := fetchPRBranch(w, c); err != nil {
			if warnErr := writef(w, "Warning: %v\n", err); warnErr != nil {
				return warnErr
			}
		}
	}
	if err := noteIfNotAtHead(w, c, wt.HEAD); err != nil {
		return err
	}

	execErr := executePostCreateCommand(w, cmdExec, cmd.String("exec"), wt.Path)

	stay := cmd.Bool("stay")
	if !stay {
		if err := marker.Emit(errW, procenv.From(ctx).Environ, wt.Path); err != nil {
			return err
		}
	}

	if execErr != nil {
		return fmt.Errorf("--exec command failed in existing worktree '%s': %w", wt.Path, execErr)
	}

	if err := writef(w, "✅ Using existing worktree for PR #%d\n\n📁 Location: %s\n🌿 Branch: %s\n\n",
		c.pr.Number, wt.Path, c.branch); err != nil {
		return err
	}
	if stay {
		return writef(w, "💡 To switch to the worktree, run:\n   wtp cd %s\n", c.branch)
	}
	return writef(w, "📍 Changed to worktree directory.\n")
}

func writef(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format, args...)
	return err
}

func shortSHA(sha string) string {
	if len(sha) > shortSHALen {
		return sha[:shortSHALen]
	}
	return sha
}
