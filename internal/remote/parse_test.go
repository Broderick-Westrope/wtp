package remote_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/satococoa/wtp/v3/internal/remote"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		want      remote.RepoIdentifier
		wantError bool
	}{
		{
			name:  "https with .git suffix",
			input: "https://github.com/owner/repo.git",
			want:  remote.RepoIdentifier{Host: "", Owner: "owner", Repo: "repo"},
		},
		{
			name:  "https without .git suffix",
			input: "https://github.com/owner/repo",
			want:  remote.RepoIdentifier{Host: "", Owner: "owner", Repo: "repo"},
		},
		{
			name:  "SCP-style git@",
			input: "git@github.com:owner/repo.git",
			want:  remote.RepoIdentifier{Host: "", Owner: "owner", Repo: "repo"},
		},
		{
			name:  "ssh scheme",
			input: "ssh://git@github.com/owner/repo.git",
			want:  remote.RepoIdentifier{Host: "", Owner: "owner", Repo: "repo"},
		},
		{
			name:  "ssh scheme with port (port ignored)",
			input: "ssh://git@github.com:2222/owner/repo.git",
			want:  remote.RepoIdentifier{Host: "", Owner: "owner", Repo: "repo"},
		},
		{
			name:  "GHE https",
			input: "https://corp.example.com/org/repo.git",
			want:  remote.RepoIdentifier{Host: "corp.example.com", Owner: "org", Repo: "repo"},
		},
		{
			name:  "GHE SCP-style",
			input: "git@corp.example.com:org/repo.git",
			want:  remote.RepoIdentifier{Host: "corp.example.com", Owner: "org", Repo: "repo"},
		},
		{
			name:      "empty URL",
			input:     "",
			wantError: true,
		},
		{
			name:      "URL with only one path segment",
			input:     "https://github.com/onlyone",
			wantError: true,
		},
		{
			name:      "SCP-style with only one path segment",
			input:     "git@github.com:onlyone",
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := remote.Parse(tc.input)
			if tc.wantError {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRepoIdentifier_StoragePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   remote.RepoIdentifier
		want string
	}{
		{
			name: "github.com repo",
			id:   remote.RepoIdentifier{Host: "", Owner: "owner", Repo: "repo"},
			want: "owner/repo",
		},
		{
			name: "GHE repo",
			id:   remote.RepoIdentifier{Host: "corp.example.com", Owner: "org", Repo: "repo"},
			want: "corp.example.com/org/repo",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.id.StoragePath())
		})
	}
}

func TestRepoIdentifier_StateKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		id     remote.RepoIdentifier
		branch string
		want   string
	}{
		{
			name:   "github.com repo simple branch",
			id:     remote.RepoIdentifier{Host: "", Owner: "owner", Repo: "repo"},
			branch: "main",
			want:   "owner/repo::main",
		},
		{
			name:   "github.com repo slash in branch",
			id:     remote.RepoIdentifier{Host: "", Owner: "owner", Repo: "repo"},
			branch: "feature/auth",
			want:   "owner/repo::feature/auth",
		},
		{
			name:   "GHE repo",
			id:     remote.RepoIdentifier{Host: "corp.example.com", Owner: "org", Repo: "repo"},
			branch: "main",
			want:   "corp.example.com/org/repo::main",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.id.StateKey(tc.branch))
		})
	}
}

func TestParseStateKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        string
		wantRepo   string
		wantBranch string
	}{
		{
			name:       "simple branch",
			key:        "owner/repo::main",
			wantRepo:   "owner/repo",
			wantBranch: "main",
		},
		{
			name:       "branch with slash",
			key:        "owner/repo::feature/auth",
			wantRepo:   "owner/repo",
			wantBranch: "feature/auth",
		},
		{
			name:       "branch with double colon preserved",
			key:        "owner/repo::fix::hotfix",
			wantRepo:   "owner/repo",
			wantBranch: "fix::hotfix",
		},
		{
			name:       "GHE key",
			key:        "corp.example.com/org/repo::main",
			wantRepo:   "corp.example.com/org/repo",
			wantBranch: "main",
		},
		{
			name:       "no separator returns key as repo and empty branch",
			key:        "owner/repo",
			wantRepo:   "owner/repo",
			wantBranch: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotRepo, gotBranch := remote.ParseStateKey(tc.key)
			assert.Equal(t, tc.wantRepo, gotRepo)
			assert.Equal(t, tc.wantBranch, gotBranch)
		})
	}
}
