package main

import "testing"

func TestAntigravityHook(t *testing.T) {
	t.Setenv("BIRDIE_ACP_AGENT", "")
	for _, c := range []struct {
		in      antigravityHookInput
		event   string
		ignored bool
	}{
		{antigravityHookInput{ConversationID: "c", TerminationReason: "NO_TOOL_CALL"}, "stop", false},
		{antigravityHookInput{ConversationID: "c", TerminationReason: "USER_CANCELED"}, "stop", true},
		{antigravityHookInput{ConversationID: "c", TerminationReason: "ERROR"}, "stop", true},
		{antigravityHookInput{ConversationID: "c"}, "pre-invocation", false},
		{antigravityHookInput{}, "pre-invocation", true},
	} {
		if got := c.in.ignored(c.event); got != c.ignored {
			t.Errorf("%+v %s: ignored = %v", c.in, c.event, got)
		}
	}
	t.Setenv("BIRDIE_ACP_AGENT", "antigravity-headless")
	if !(antigravityHookInput{ConversationID: "c"}).ignored("pre-invocation") {
		t.Error("a headless run birdie started should not take messages")
	}
	if out, err := antigravityHookOutput("stop", "x"); err != nil || out != `{"decision":"continue","reason":"x"}` {
		t.Errorf("stop output = %q, %v", out, err)
	}
	if out, err := antigravityHookOutput("pre-invocation", "x"); err != nil || out != `{"injectSteps":[{"systemMessage":{"systemMessage":"x"}}]}` {
		t.Errorf("pre-invocation output = %q, %v", out, err)
	}
	if _, err := antigravityHookOutput("post-tool-use", "x"); err == nil {
		t.Error("unknown event accepted")
	}
}
