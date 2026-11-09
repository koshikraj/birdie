package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitBranch(t *testing.T) {
	root := t.TempDir()
	mk := func(path, content string) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("repo/.git/HEAD", "ref: refs/heads/fix/login\n")
	mk("repo/src/app/main.go", "")
	// A worktree's .git file points at its folder inside the main repository.
	mk("repo/.git/worktrees/wt/HEAD", "ref: refs/heads/status\n")
	mk("wt/.git", "gitdir: ../repo/.git/worktrees/wt\n")
	mk("detached/.git/HEAD", "4454aa9d0c1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a\n")
	os.MkdirAll(filepath.Join(root, "plain"), 0o755)

	for dir, want := range map[string]string{
		"repo":         "fix/login",
		"repo/src/app": "fix/login",
		"wt":           "status",
		"detached":     "4454aa9",
		"":             "",
	} {
		path := filepath.Join(root, dir)
		if dir == "" {
			path = ""
		}
		if got := gitBranch(path); got != want {
			t.Errorf("gitBranch(%q) = %q, want %q", dir, got, want)
		}
	}
	// Outside any repository there is no branch, as long as the temp folder
	// itself isn't in one.
	if gitBranch(os.TempDir()) == "" {
		if got := gitBranch(filepath.Join(root, "plain")); got != "" {
			t.Errorf("plain folder: %q", got)
		}
	}
}
