package github_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/github"
)

func TestParsePRRef(t *testing.T) {
	tests := map[string]struct {
		input   string
		want    github.PRRef
		wantArg string
		wantErr string
	}{
		"bare number":       {input: "123", want: github.PRRef{Number: 123}, wantArg: "123"},
		"hash number":       {input: "#42", want: github.PRRef{Number: 42}, wantArg: "42"},
		"surrounding space": {input: " 7 ", want: github.PRRef{Number: 7}, wantArg: "7"},
		"github URL": {
			input:   "https://github.com/owner/repo/pull/123",
			want:    github.PRRef{Owner: "owner", Repo: "repo", Number: 123},
			wantArg: "https://github.com/owner/repo/pull/123",
		},
		"URL with files tab": {
			input:   "https://github.com/owner/repo/pull/9/files",
			want:    github.PRRef{Owner: "owner", Repo: "repo", Number: 9},
			wantArg: "https://github.com/owner/repo/pull/9",
		},
		"enterprise URL": {
			input:   "https://ghe.example.com/team/svc/pull/5",
			want:    github.PRRef{Owner: "team", Repo: "svc", Number: 5},
			wantArg: "https://ghe.example.com/team/svc/pull/5",
		},
		"zero":           {input: "0", wantErr: "invalid pull request number"},
		"branch name":    {input: "feature/x", wantErr: "not a pull request number or URL"},
		"issue URL":      {input: "https://github.com/owner/repo/issues/3", wantErr: "not a pull request URL"},
		"non-numeric PR": {input: "https://github.com/owner/repo/pull/abc", wantErr: "invalid pull request number"},
		"linear URL":     {input: "https://linear.app/acme/issue/ENG-1", wantErr: "not a pull request URL"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := github.ParsePRRef(tc.input)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want.Owner, got.Owner)
			assert.Equal(t, tc.want.Repo, got.Repo)
			assert.Equal(t, tc.want.Number, got.Number)
			assert.Equal(t, tc.wantArg, got.Arg())
		})
	}
}
