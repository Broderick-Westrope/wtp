// Package github provides helpers for interacting with the GitHub CLI (gh).
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	cmdTimeout  = 10 * time.Second
	stateReady  = "OPEN"
	stateDraft  = "DRAFT"
	stateClosed = "CLOSED"

	// StateMerged is the PR state for merged pull requests. Exported for use
	// by callers that need to check PR state (e.g. auto-archive logic).
	StateMerged = "MERGED"

	checkStatePassing = "pass"
	checkStateFailing = "fail"
	checkStatePending = "pending"
	checkStateSkipped = "skipped"
)

// PRInfo holds pull request metadata fetched from the GitHub CLI.
type PRInfo struct {
	Number     int
	State      string
	Title      string
	HeadBranch string
	IsDraft    bool
}

// CIStatus holds aggregated CI check status for a pull request.
type CIStatus struct {
	State   string
	Total   int
	Passing int
	Failing int
	Pending int
}

// prViewResponse is the JSON structure returned by `gh pr view --json`.
type prViewResponse struct {
	Number      int    `json:"number"`
	State       string `json:"state"`
	Title       string `json:"title"`
	HeadRefName string `json:"headRefName"`
	IsDraft     bool   `json:"isDraft"`
}

// checkEntry is one item in the JSON array returned by `gh pr checks --json`.
type checkEntry struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// IsAvailable reports whether the `gh` CLI is on the PATH.
func IsAvailable() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// IsAuthenticated reports whether the `gh` CLI is authenticated.
// It runs `gh auth status` and checks the exit code.
func IsAuthenticated() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		// Non-zero exit = not authenticated (or gh unavailable)
		var exitErr *exec.ExitError
		if ok := isExitError(err, &exitErr); ok {
			return false, nil
		}

		return false, fmt.Errorf("running gh auth status: %w", err)
	}

	return true, nil
}

// GetPRForBranch fetches pull request metadata for the given branch.
// Returns (nil, nil) if no PR exists for the branch.
func GetPRForBranch(ctx context.Context, branch string) (*PRInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "pr", "view",
		"--json", "number,state,title,headRefName,isDraft", "--", branch)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if ok := isExitError(err, &exitErr); ok {
			// gh exits 1 with "no pull requests found" when there is no PR
			if strings.Contains(strings.ToLower(stderr.String()), "no pull requests found") {
				return nil, nil
			}
		}

		return nil, fmt.Errorf("running gh pr view: %w (stderr: %s)", err, stderr.String())
	}

	var resp prViewResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parsing gh pr view output: %w", err)
	}

	pr := &PRInfo{
		Number:     resp.Number,
		State:      resp.State,
		Title:      resp.Title,
		HeadBranch: resp.HeadRefName,
		IsDraft:    resp.IsDraft,
	}

	// Override state for drafts so callers see a single canonical value.
	if resp.IsDraft && resp.State == stateReady {
		pr.State = stateDraft
	}

	return pr, nil
}

// GetCIStatus fetches aggregated CI check status for the given branch.
// Returns (nil, nil) if there is no PR or no checks are present.
func GetCIStatus(ctx context.Context, branch string) (*CIStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "pr", "checks",
		"--json", "name,state", "--", branch)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if ok := isExitError(err, &exitErr); ok {
			stderrLower := strings.ToLower(stderr.String())
			if strings.Contains(stderrLower, "no pull requests found") ||
				strings.Contains(stderrLower, "no checks reported") {
				return nil, nil
			}
		}

		return nil, fmt.Errorf("running gh pr checks: %w (stderr: %s)", err, stderr.String())
	}

	var checks []checkEntry
	if err := json.Unmarshal(stdout.Bytes(), &checks); err != nil {
		return nil, fmt.Errorf("parsing gh pr checks output: %w", err)
	}

	if len(checks) == 0 {
		return nil, nil
	}

	ci := &CIStatus{Total: len(checks)}
	for _, c := range checks {
		switch strings.ToLower(c.State) {
		case checkStatePassing, "success", "completed":
			ci.Passing++
		case checkStateFailing, "failure", "error", "timed_out", "canceled", "action_required":
			ci.Failing++
		case checkStatePending, "queued", "in_progress", "waiting", "requested":
			ci.Pending++
		case checkStateSkipped, "neutral":
			// skipped checks don't count toward pass/fail/pending
			ci.Total-- // adjust total to exclude skipped
		default:
			ci.Pending++ // treat unknown states as pending
		}
	}

	// Derive overall state
	switch {
	case ci.Failing > 0:
		ci.State = "failing"
	case ci.Pending > 0:
		ci.State = "pending"
	default:
		ci.State = "passing"
	}

	return ci, nil
}

// FormatPRState returns a human-readable string for a PR, e.g. "#42 Ready".
// Returns "-" if pr is nil.
func FormatPRState(pr *PRInfo) string {
	if pr == nil {
		return "-"
	}

	var label string

	switch pr.State {
	case StateMerged:
		label = "Merged"
	case stateClosed:
		label = "Closed"
	case stateDraft:
		label = "Draft"
	default:
		// OPEN (and draft-override already applied above in GetPRForBranch)
		label = "Ready"
	}

	return fmt.Sprintf("#%d %s", pr.Number, label)
}

// FormatCIStatus returns a human-readable CI status string.
// Returns "-" if ci is nil or there are no checks.
func FormatCIStatus(ci *CIStatus) string {
	if ci == nil || ci.Total == 0 {
		return "-"
	}

	switch {
	case ci.Failing > 0:
		return fmt.Sprintf("✗ %d/%d failing", ci.Failing, ci.Total)
	case ci.Pending > 0:
		return fmt.Sprintf("● %d pending", ci.Pending)
	default:
		return "✓ CI passing"
	}
}

// isExitError is a helper that avoids importing exec in the caller via a type
// assertion and returns whether err is an *exec.ExitError.
func isExitError(err error, target **exec.ExitError) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if target != nil {
			*target = ee
		}

		return true
	}

	return false
}
