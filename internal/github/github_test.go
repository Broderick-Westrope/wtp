package github_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/github"
)

// --------------------------------------------------------------------------
// FormatPRState
// --------------------------------------------------------------------------

func TestFormatPRState_nil(t *testing.T) {
	assert.Equal(t, "-", github.FormatPRState(nil))
}

func TestFormatPRState_ready(t *testing.T) {
	pr := &github.PRInfo{Number: 42, State: "OPEN"}
	assert.Equal(t, "#42 Ready", github.FormatPRState(pr))
}

func TestFormatPRState_draft(t *testing.T) {
	pr := &github.PRInfo{Number: 7, State: "DRAFT", IsDraft: true}
	assert.Equal(t, "#7 Draft", github.FormatPRState(pr))
}

func TestFormatPRState_merged(t *testing.T) {
	pr := &github.PRInfo{Number: 99, State: "MERGED"}
	assert.Equal(t, "#99 Merged", github.FormatPRState(pr))
}

func TestFormatPRState_closed(t *testing.T) {
	pr := &github.PRInfo{Number: 5, State: "CLOSED"}
	assert.Equal(t, "#5 Closed", github.FormatPRState(pr))
}

// --------------------------------------------------------------------------
// FormatCIStatus
// --------------------------------------------------------------------------

func TestFormatCIStatus_nil(t *testing.T) {
	assert.Equal(t, "-", github.FormatCIStatus(nil))
}

func TestFormatCIStatus_noChecks(t *testing.T) {
	ci := &github.CIStatus{Total: 0}
	assert.Equal(t, "-", github.FormatCIStatus(ci))
}

func TestFormatCIStatus_passing(t *testing.T) {
	ci := &github.CIStatus{State: "passing", Total: 5, Passing: 5}
	assert.Equal(t, "✓ CI passing", github.FormatCIStatus(ci))
}

func TestFormatCIStatus_failing(t *testing.T) {
	ci := &github.CIStatus{State: "failing", Total: 5, Passing: 3, Failing: 2}
	assert.Equal(t, "✗ 2/5 failing", github.FormatCIStatus(ci))
}

func TestFormatCIStatus_pending(t *testing.T) {
	ci := &github.CIStatus{State: "pending", Total: 3, Pending: 3}
	assert.Equal(t, "● 3 pending", github.FormatCIStatus(ci))
}

func TestFormatCIStatus_pendingMixed(t *testing.T) {
	// Some passing, some pending — still shows pending
	ci := &github.CIStatus{State: "pending", Total: 5, Passing: 2, Pending: 3}
	assert.Equal(t, "● 3 pending", github.FormatCIStatus(ci))
}

// --------------------------------------------------------------------------
// JSON parsing helpers (unit tests for the shape of gh output)
// --------------------------------------------------------------------------

// prViewFixture mirrors the raw JSON that `gh pr view --json ...` emits.
type prViewFixture struct {
	Number      int    `json:"number"`
	State       string `json:"state"`
	Title       string `json:"title"`
	HeadRefName string `json:"headRefName"`
	IsDraft     bool   `json:"isDraft"`
}

func TestPRViewJSONParsing_openPR(t *testing.T) {
	raw := `{"number":42,"state":"OPEN","title":"Add feature","headRefName":"feature/auth","isDraft":false}`

	var resp prViewFixture
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))

	assert.Equal(t, 42, resp.Number)
	assert.Equal(t, "OPEN", resp.State)
	assert.Equal(t, "Add feature", resp.Title)
	assert.Equal(t, "feature/auth", resp.HeadRefName)
	assert.False(t, resp.IsDraft)
}

func TestPRViewJSONParsing_draftPR(t *testing.T) {
	raw := `{"number":7,"state":"OPEN","title":"WIP: draft","headRefName":"fix/login","isDraft":true}`

	var resp prViewFixture
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))

	assert.Equal(t, 7, resp.Number)
	assert.True(t, resp.IsDraft)
}

func TestPRViewJSONParsing_mergedPR(t *testing.T) {
	raw := `{"number":99,"state":"MERGED","title":"Merged PR","headRefName":"old-feature","isDraft":false}`

	var resp prViewFixture
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))

	assert.Equal(t, "MERGED", resp.State)
}

// checkEntryFixture mirrors what `gh pr checks --json name,state` emits per entry.
type checkEntryFixture struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

func TestPRChecksJSONParsing(t *testing.T) {
	raw := `[
		{"name":"ci/build","state":"pass"},
		{"name":"ci/test","state":"fail"},
		{"name":"ci/lint","state":"pending"}
	]`

	var checks []checkEntryFixture
	require.NoError(t, json.Unmarshal([]byte(raw), &checks))

	require.Len(t, checks, 3)
	assert.Equal(t, "pass", checks[0].State)
	assert.Equal(t, "fail", checks[1].State)
	assert.Equal(t, "pending", checks[2].State)
}

// --------------------------------------------------------------------------
// GetPRForBranch — "no pull requests found" path
// --------------------------------------------------------------------------

// TestGetPRForBranch_noPRReturnsNil exercises the nil-nil contract when the
// gh CLI is unavailable (not installed). In CI / dev environments where gh is
// not present IsAvailable() returns false, and we verify the function behaves
// predictably by checking the error message rather than blindly requiring nil.
//
// When gh IS installed and authenticated, the real network call would run; we
// skip that scenario in unit tests (it belongs in e2e tests).
func TestGetPRForBranch_ghNotAvailable(t *testing.T) {
	if github.IsAvailable() {
		t.Skip("gh CLI is installed; skipping unavailability test")
	}

	pr, err := github.GetPRForBranch(context.Background(), "some-branch")
	// Without gh we expect an error (exec: not found), not a nil PR.
	assert.Nil(t, pr)
	assert.Error(t, err)
}

// TestGetCIStatus_ghNotAvailable mirrors the above for GetCIStatus.
func TestGetCIStatus_ghNotAvailable(t *testing.T) {
	if github.IsAvailable() {
		t.Skip("gh CLI is installed; skipping unavailability test")
	}

	ci, err := github.GetCIStatus(context.Background(), "some-branch")
	assert.Nil(t, ci)
	assert.Error(t, err)
}
