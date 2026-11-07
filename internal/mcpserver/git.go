package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
)

// gitBranch returns the branch checked out in the git repository holding dir,
// the short commit when no branch is, or "" outside a repository. It reads the
// files directly, because it runs on every report and git may not be installed.
func gitBranch(dir string) string {
	if dir == "" {
		return ""
	}
	for {
		dotGit := filepath.Join(dir, ".git")
		if fi, err := os.Stat(dotGit); err == nil {
			if fi.IsDir() {
				return headBranch(dotGit)
			}
			// A worktree or submodule: .git is a file naming the real folder.
			return headBranch(gitdirFile(dotGit))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func gitdirFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(b), "\n")
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return ""
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(path), gitdir)
	}
	return gitdir
}

func headBranch(gitdir string) string {
	if gitdir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		return strings.TrimPrefix(ref, "refs/heads/")
	}
	if len(head) >= 7 {
		return head[:7]
	}
	return ""
}
