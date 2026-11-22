package mcpserver

import (
	"strings"
	"testing"
)

func TestHasChannel(t *testing.T) {
	cases := map[string]bool{
		"claude --dangerously-load-development-channels server:birdie":                   true,
		"claude --dangerously-load-development-channels=server:birdie":                   true,
		"claude --dangerously-load-development-channels server:webhook server:birdie -c": true,
		"claude --channels plugin:telegram@x,server:birdie":                              true,
		"claude --dangerously-load-development-channels server:webhook":                  false,
		"claude --dangerously-load-development-channels server:birdie-listen":            false,
		"claude --resume server:birdie":                                                  false,
		"claude":                                                                         false,
	}
	for cmd, want := range cases {
		if got := hasChannel(strings.Fields(cmd), "birdie"); got != want {
			t.Errorf("hasChannel(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestIsClaudeCode(t *testing.T) {
	cases := map[string]bool{
		"claude --resume":           true,
		"/home/u/.local/bin/claude": true,
		"node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js": true,
		"/bin/sh wrapper.sh": false,
		"node server.js":     false,
	}
	for cmd, want := range cases {
		if got := isClaudeCode(strings.Fields(cmd)); got != want {
			t.Errorf("isClaudeCode(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestParsePSKeepsSpacesInThePath(t *testing.T) {
	app := "/Users/u/Library/Application Support/Claude/claude-code/2.1.0/x/claude.app/Contents/MacOS/claude"
	cases := []struct {
		head, args string
		ppid       int
		argv       []string
	}{
		{" 16419 " + app + "\n", app + " --output-format stream-json --dangerously-load-development-channels server:birdie\n",
			16419, []string{app, "--output-format", "stream-json", "--dangerously-load-development-channels", "server:birdie"}},
		// Where comm and args disagree (not macOS), args split on spaces, as before.
		{"1 /usr/local/bin/node", "node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js --resume",
			1, []string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js", "--resume"}},
		{"3 node", "claude --dangerously-load-development-channels server:birdie",
			3, []string{"claude", "--dangerously-load-development-channels", "server:birdie"}},
		{"500 /bin/zsh", "/bin/zsh", 500, []string{"/bin/zsh"}},
		// comm a prefix of a longer first word isn't argv[0].
		{"9 /x/claude", "/x/claude-helper --y", 9, []string{"/x/claude-helper", "--y"}},
		// No comm, but args to go on.
		{"4 ", "/x/claude --y", 4, []string{"/x/claude", "--y"}},
	}
	for _, c := range cases {
		ppid, argv, err := parsePS(7, c.head, c.args)
		if err != nil || ppid != c.ppid || strings.Join(argv, "|") != strings.Join(c.argv, "|") {
			t.Errorf("parsePS(%q, %q) = %d, %q, %v", c.head, c.args, ppid, argv, err)
		}
	}
	_, argv, _ := parsePS(7, cases[0].head, cases[0].args)
	if !isClaudeCode(argv) || !hasChannel(argv, "birdie") {
		t.Errorf("the desktop app's Claude Code wasn't recognised: %q", argv)
	}
	if _, argv, _ := parsePS(7, cases[2].head, cases[2].args); !isClaudeCode(argv) || !hasChannel(argv, "birdie") {
		t.Errorf("Claude Code whose comm and args disagree wasn't recognised: %q", argv)
	}
	if _, _, err := parsePS(7, "", ""); err == nil {
		t.Error("a process that has gone should be an error")
	}
}
