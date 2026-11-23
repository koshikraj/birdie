package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"birdie/internal/store"
)

func TestConfig(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "bus.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runConfig(ctx, &out, st, args)
		return out.String(), err
	}
	for _, step := range []struct{ args, want string }{
		{"list", "12 (default)"},
		{"get loop-limit", "12\n"},
		{"set loop-limit 60", "loop-limit is now 60."},
		{"get loop-limit", "60\n"},
		{"set loop-limit 0", "now 0"},
		{"unset loop-limit", "default, 12"},
		{"get loop-limit", "12\n"},
	} {
		got, err := run(strings.Fields(step.args)...)
		if err != nil || !strings.Contains(got, step.want) {
			t.Errorf("config %s = %q, %v; want %q", step.args, got, err, step.want)
		}
	}
	for _, bad := range []string{"set loop-limit -1", "set loop-limit lots", "set loop-limit", "get nothing", "set", "what"} {
		if got, err := run(strings.Fields(bad)...); err == nil {
			t.Errorf("config %s = %q, want an error", bad, got)
		}
	}
	if got, _ := run("get", "loop-limit"); got != "12\n" {
		t.Errorf("a refused value was saved: %q", got)
	}
}
