package bus

import "testing"

func TestLabelOf(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]string
		want   string
	}{
		{"reviewer", map[string]string{"label": "Claude Code"}, "Claude Code"},
		{"claude", map[string]string{"label": "  Review bot "}, "Review bot"}, // a saved label wins
		{"grok-tui", nil, "Grok CLI"},
		{"grok", nil, "Grok CLI (headless)"},
		{"grok-bot", map[string]string{"label": " "}, "Grok Bot"},
		{"me", nil, "You"},
		{"reviewer", nil, "reviewer"},
	}
	for _, c := range cases {
		if got := LabelOf(c.name, c.config); got != c.want {
			t.Errorf("LabelOf(%q, %v) = %q, want %q", c.name, c.config, got, c.want)
		}
	}
	if got := (Agent{Name: "codex"}).DisplayName(); got != "Codex" {
		t.Errorf("DisplayName = %q", got)
	}
}
