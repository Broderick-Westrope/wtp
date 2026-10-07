package maintenance

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

const lsofTimeout = 10 * time.Second

// DiscoverRepos returns the main worktree path of every repository that has a
// worktree under root (wtp's centralized worktree storage). Each linked
// worktree's .git file points at <main>/.git/worktrees/<name>, so repositories
// are found without a registry and without running git. The walk stops at
// each worktree root and never descends into its contents.
func DiscoverRepos(root string) ([]string, error) {
	seen := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root && errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipAll
			}
			return nil //nolint:nilerr // unreadable subtrees are skipped, not fatal
		}
		if !d.IsDir() {
			return nil
		}
		gitFile := filepath.Join(path, ".git")
		info, statErr := os.Lstat(gitFile)
		if statErr != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			if main, ok := mainRepoFromGitFile(gitFile); ok {
				seen[main] = true
			}
		}
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}

	repos := make([]string, 0, len(seen))
	for repo := range seen {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos, nil
}

func mainRepoFromGitFile(gitFile string) (string, bool) {
	data, err := os.ReadFile(gitFile) //nolint:gosec // path comes from walking wtp's own storage
	if err != nil {
		return "", false
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return "", false
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(gitFile), gitDir)
	}
	worktreesDir := filepath.Dir(filepath.Clean(gitDir))
	commonDir := filepath.Dir(worktreesDir)
	if filepath.Base(worktreesDir) != "worktrees" || filepath.Base(commonDir) != ".git" {
		return "", false
	}
	if _, err := os.Stat(commonDir); err != nil {
		return "", false
	}
	return filepath.Dir(commonDir), true
}

// processWorkingDirs returns the working directories of the current user's
// processes, via one lsof call. It returns nil when lsof is unavailable, in
// which case the in-use check is skipped rather than blocking every archive.
func processWorkingDirs(ctx context.Context) []string {
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		// macOS ships lsof in /usr/sbin, which a minimal PATH may omit.
		if _, statErr := os.Stat("/usr/sbin/lsof"); statErr != nil {
			return nil
		}
		lsof = "/usr/sbin/lsof"
	}
	ctx, cancel := context.WithTimeout(ctx, lsofTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, lsof, "-a", "-d", "cwd", "-u", strconv.Itoa(os.Getuid()), "-Fn")
	cmd.Env = procenv.From(ctx).Environ
	// lsof exits non-zero when it cannot inspect some processes; the output it
	// did produce is still valid.
	out, _ := cmd.Output()
	return parseLsofNames(out)
}

func parseLsofNames(out []byte) []string {
	var dirs []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		if name, ok := strings.CutPrefix(scanner.Text(), "n"); ok && name != "" {
			dirs = append(dirs, name)
		}
	}
	return dirs
}
