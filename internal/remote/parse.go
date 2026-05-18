// Package remote provides parsing of Git remote URLs into structured repo identifiers.
package remote

import (
	"fmt"
	"net/url"
	"strings"
)

const githubHost = "github.com"

// RepoIdentifier holds the parsed components of a Git remote URL.
// Host is empty for github.com repositories; non-empty for GitHub Enterprise or other hosts.
type RepoIdentifier struct {
	Host  string
	Owner string
	Repo  string
}

// Parse parses a Git remote URL into a RepoIdentifier. It handles the following formats:
//   - https://github.com/owner/repo.git
//   - https://github.com/owner/repo
//   - git@github.com:owner/repo.git  (SCP-style)
//   - ssh://git@github.com/owner/repo.git
//   - ssh://git@github.com:2222/owner/repo.git  (port ignored)
//
// The .git suffix is always stripped. For non-github.com hosts the Host field is set.
func Parse(remoteURL string) (RepoIdentifier, error) {
	if remoteURL == "" {
		return RepoIdentifier{}, fmt.Errorf("remote URL must not be empty")
	}

	var host, owner, repo string

	// Detect SCP-style URLs (git@host:path) — no scheme present.
	if !strings.Contains(remoteURL, "://") && strings.Contains(remoteURL, ":") {
		parsed, err := parseSCP(remoteURL)
		if err != nil {
			return RepoIdentifier{}, err
		}

		host, owner, repo = parsed.Host, parsed.Owner, parsed.Repo
	} else {
		parsed, err := url.Parse(remoteURL)
		if err != nil {
			return RepoIdentifier{}, fmt.Errorf("invalid remote URL %q: %w", remoteURL, err)
		}

		// Strip port from host.
		h := parsed.Hostname()

		// Normalise the path: trim leading slash and .git suffix.
		p := strings.TrimPrefix(parsed.Path, "/")
		p = strings.TrimSuffix(p, ".git")

		segments := strings.SplitN(p, "/", 3) //nolint:mnd // split owner/repo
		if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
			return RepoIdentifier{}, fmt.Errorf("remote URL %q must contain at least owner/repo path segments", remoteURL)
		}

		host = h
		owner = segments[0]
		repo = segments[1]
	}

	// Normalise github.com host to empty string.
	if host == githubHost {
		host = ""
	}

	return RepoIdentifier{Host: host, Owner: owner, Repo: repo}, nil
}

// parseSCP parses an SCP-style remote URL like git@host:owner/repo.git.
func parseSCP(remoteURL string) (RepoIdentifier, error) {
	// Split on the first ':' to separate host from path.
	colonIdx := strings.Index(remoteURL, ":")
	if colonIdx < 0 {
		return RepoIdentifier{}, fmt.Errorf("invalid SCP-style URL %q: missing ':'", remoteURL)
	}

	hostPart := remoteURL[:colonIdx]
	pathPart := remoteURL[colonIdx+1:]

	// hostPart may be "user@host" — strip user portion.
	if atIdx := strings.LastIndex(hostPart, "@"); atIdx >= 0 {
		hostPart = hostPart[atIdx+1:]
	}

	pathPart = strings.TrimSuffix(pathPart, ".git")

	segments := strings.SplitN(pathPart, "/", 3) //nolint:mnd // split owner/repo
	if len(segments) < 2 || segments[0] == "" || segments[1] == "" {
		return RepoIdentifier{}, fmt.Errorf("SCP-style URL %q must contain owner/repo path", remoteURL)
	}

	return RepoIdentifier{Host: hostPart, Owner: segments[0], Repo: segments[1]}, nil
}

// StoragePath returns the filesystem path segment for this repo.
// For github.com repos it is "owner/repo"; for other hosts it is "host/owner/repo".
func (r RepoIdentifier) StoragePath() string {
	if r.Host == "" {
		return r.Owner + "/" + r.Repo
	}

	return r.Host + "/" + r.Owner + "/" + r.Repo
}

// StateKey returns a unique string key combining the repo's storage path and a branch name,
// separated by "::". Slashes in the branch name are preserved.
func (r RepoIdentifier) StateKey(branch string) string {
	return r.StoragePath() + "::" + branch
}

// ParseStateKey splits a state key into its repo path and branch components.
// The split is performed on the first "::" so that branch names containing "::" are preserved.
// Example: "owner/repo::fix::hotfix" → ("owner/repo", "fix::hotfix").
func ParseStateKey(key string) (repoPath, branch string) {
	parts := strings.SplitN(key, "::", 2) //nolint:mnd // split on first ::
	if len(parts) != 2 {
		return key, ""
	}

	return parts[0], parts[1]
}
