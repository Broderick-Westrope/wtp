package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Broderick-Westrope/wtp/v3/test/e2e/framework"
)

func TestHookOutputStreaming(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("hook-streaming")

	// In v3 the config is hooks-only (no version field).
	config := map[string]any{
		"hooks": map[string]any{
			"post_create": []map[string]any{
				{
					"type":    "command",
					"command": "echo 'Starting hook...'; sleep 0.1; echo 'Processing...'; sleep 0.1; echo 'Completed!'",
				},
			},
		},
	}

	configData, err := yaml.Marshal(config)
	framework.AssertNoError(t, err)

	configPath := filepath.Join(repo.Path(), ".wtp.yml")
	framework.AssertNoError(t, os.WriteFile(configPath, configData, 0644))

	// Run add command with -b flag to create new branch and capture output
	output, err := repo.RunWTP("add", "-b", "test-branch")
	framework.AssertNoError(t, err)

	// Verify output contains all expected messages in order
	framework.AssertOutputContains(t, output, "Executing post-create hooks...")
	framework.AssertOutputContains(t, output, "Starting hook...")
	framework.AssertOutputContains(t, output, "Processing...")
	framework.AssertOutputContains(t, output, "Completed!")
	framework.AssertOutputContains(t, output, "✓ All hooks executed successfully")

	// In v3 worktrees are stored in centralized XDG storage.
	worktreePath := repo.CentralizedWorktreePath("test-branch")
	framework.AssertWorktreeExists(t, repo, worktreePath)
}
