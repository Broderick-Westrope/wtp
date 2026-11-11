package github

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// PRRef identifies a pull request by its base repository and number.
// Owner and Repo are empty when the reference was a bare number, which gh
// resolves against the repository it runs in.
type PRRef struct {
	Owner  string
	Repo   string
	Number int

	url string
}

// Arg returns the reference in a form `gh pr view` accepts: the number, or
// the canonical PR URL without any trailing path or query.
func (r PRRef) Arg() string {
	if r.Owner == "" {
		return strconv.Itoa(r.Number)
	}
	return r.url
}

// ParsePRRef parses a pull request number ("123" or "#123") or a PR web URL
// ("https://github.com/owner/repo/pull/123", optionally with a trailing path
// such as "/files"). Any host is accepted so GitHub Enterprise URLs work.
func ParsePRRef(s string) (PRRef, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(strings.TrimPrefix(s, "#")); err == nil {
		if n <= 0 {
			return PRRef{}, fmt.Errorf("invalid pull request number %q", s)
		}
		return PRRef{Number: n}, nil
	}

	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return PRRef{}, fmt.Errorf("%q is not a pull request number or URL", s)
	}

	// owner/repo/pull/<n>[/...]
	const minSegments = 4
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) < minSegments || segments[2] != "pull" || segments[0] == "" || segments[1] == "" {
		return PRRef{}, fmt.Errorf("%q is not a pull request URL (expected .../<owner>/<repo>/pull/<number>)", s)
	}
	n, err := strconv.Atoi(segments[3])
	if err != nil || n <= 0 {
		return PRRef{}, fmt.Errorf("%q has an invalid pull request number", s)
	}

	return PRRef{
		Owner:  segments[0],
		Repo:   segments[1],
		Number: n,
		url:    fmt.Sprintf("%s://%s/%s/%s/pull/%d", u.Scheme, u.Host, segments[0], segments[1], n),
	}, nil
}
