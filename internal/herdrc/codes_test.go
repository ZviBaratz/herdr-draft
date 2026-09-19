package herdrc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// codeSentinelCases is every herdr code this package names as a sentinel,
// beside the call that raises it.
var codeSentinelCases = []struct {
	code     string
	sentinel error
}{
	{"agent_pane_busy", ErrPaneBusy},
	{"agent_name_taken", ErrAgentNameTaken},
	{"agent_not_ready", ErrAgentNotReady},
}

// TestCLIErrorIsItsCodesSentinel: a failed call whose envelope carries one
// of the codes internal/plan branches on matches that code's sentinel, and
// no other, through the CLI's real stderr shape.
func TestCLIErrorIsItsCodesSentinel(t *testing.T) {
	for _, tc := range codeSentinelCases {
		t.Run(tc.code, func(t *testing.T) {
			bin := fakeHerdrFailEnvelope(t, tc.code, "refused")
			err := (&CLIRunner{Bin: bin}).AgentStart(context.Background(), AgentStartReq{Name: "n", Kind: "claude", PaneID: "w1:p1"})
			for _, other := range codeSentinelCases {
				if got, want := errors.Is(err, other.sentinel), other.code == tc.code; got != want {
					t.Errorf("errors.Is(err, sentinel for %s) = %v, want %v\nerror: %s", other.code, got, want, err)
				}
			}
		})
	}
}

// TestCLIErrorCodeIsNotItsText is #144. The error's text holds the argv, and
// the argv holds the caller's own text -- here a prompt that mentions every
// code -- so a failure with some OTHER code must match none of them. That
// is the difference between reading herdr's verdict and grepping a string
// the user partly wrote.
func TestCLIErrorCodeIsNotItsText(t *testing.T) {
	var mention []string
	for _, tc := range codeSentinelCases {
		mention = append(mention, tc.code)
	}
	text := "why does " + strings.Join(mention, " and ") + " happen?"

	for _, stderr := range []struct{ name, code string }{
		{"another code", "agent_prompt_failed"},
		{"a timeout", "timeout"},
	} {
		t.Run(stderr.name, func(t *testing.T) {
			bin := fakeHerdrFailEnvelope(t, stderr.code, "no")
			err := (&CLIRunner{Bin: bin}).AgentPrompt(context.Background(), AgentPromptReq{Target: "w1:p1", Text: text})
			if err == nil || !strings.Contains(err.Error(), mention[0]) {
				t.Fatalf("error = %v, want it to carry the prompt text -- the scenario needs it", err)
			}
			for _, tc := range codeSentinelCases {
				if errors.Is(err, tc.sentinel) {
					t.Errorf("errors.Is(err, sentinel for %s) = true for a %q failure whose prompt merely names it", tc.code, stderr.code)
				}
			}
		})
	}

	// And stderr that is not an envelope at all, however it reads.
	bin := fakeHerdrFail(t, "agent_pane_busy agent_name_taken agent_not_ready")
	err := (&CLIRunner{Bin: bin}).AgentStart(context.Background(), AgentStartReq{Name: "n", Kind: "claude", PaneID: "w1:p1"})
	for _, tc := range codeSentinelCases {
		if errors.Is(err, tc.sentinel) {
			t.Errorf("errors.Is(err, sentinel for %s) = true for stderr that is not an envelope", tc.code)
		}
	}
}

// TestCodeSentinelSurvivesWrapping: the classification has to reach
// internal/plan through ErrNothingCreated's mark and plan's own %w wraps.
func TestCodeSentinelSurvivesWrapping(t *testing.T) {
	bin := fakeHerdrFailEnvelope(t, "agent_pane_busy", "busy")
	err := (&CLIRunner{Bin: bin}).AgentStart(context.Background(), AgentStartReq{Name: "n", Kind: "claude", PaneID: "w1:p1"})
	wrapped := fmt.Errorf("plan: %w", &nothingCreatedError{err})
	if !errors.Is(wrapped, ErrPaneBusy) {
		t.Errorf("errors.Is(wrapped, ErrPaneBusy) = false through %q", wrapped)
	}
	if !errors.Is(wrapped, ErrNothingCreated) {
		t.Errorf("errors.Is(wrapped, ErrNothingCreated) = false -- the mark must survive too")
	}
}
