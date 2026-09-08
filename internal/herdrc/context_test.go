package herdrc

import (
	"errors"
	"strings"
	"testing"
)

// TestParseContext_UnsetIsDistinctFromMalformed pins the distinction the
// whole first-contact message rests on.
//
// An EMPTY payload almost always means a person started the binary from a
// shell with no herdr involved; a malformed one means herdr sent something
// unreadable, which is a bug and a different conversation. Collapsing them
// is what produced `parse plugin context: unexpected end of JSON input` as
// the first thing anyone saw after installing the plugin.
func TestParseContext_UnsetIsDistinctFromMalformed(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\t "} {
		if _, err := ParseContext(raw); !errors.Is(err, ErrContextUnset) {
			t.Errorf("ParseContext(%q) = %v, want ErrContextUnset", raw, err)
		}
	}

	// Malformed must NOT be reported as unset: the reader is already
	// inside a herdr context and telling them what herdr is would be
	// answering a question they did not ask.
	for _, raw := range []string{"{", "not json", `{"workspace_id":}`} {
		_, err := ParseContext(raw)
		if err == nil {
			t.Errorf("ParseContext(%q) = nil error, want a parse failure", raw)
			continue
		}
		if errors.Is(err, ErrContextUnset) {
			t.Errorf("ParseContext(%q) reported ErrContextUnset; malformed is a different situation", raw)
		}
	}
}

// TestParseContext_EmptyObjectIsAValidContext guards the boundary the
// other way. "{}" is a legitimate invocation context -- every field herdr
// sends is optional -- so absence of DATA must not be read as absence of a
// CONTEXT, which would refuse a launch herdr had made correctly.
func TestParseContext_EmptyObjectIsAValidContext(t *testing.T) {
	ctx, err := ParseContext("{}")
	if err != nil {
		t.Fatalf("ParseContext(\"{}\") = %v, want a valid empty context", err)
	}
	if ctx != (Context{}) {
		t.Errorf("ParseContext(\"{}\") = %+v, want the zero Context", ctx)
	}
}

// TestCmdError_NoDanglingSeparatorOnSilentFailure covers a message shape,
// which is the whole defect: all three run helpers appended `: %s`
// unconditionally, so a subcommand failing with nothing on stderr -- what
// an unreachable server can produce -- ended its message in a colon and a
// space. That reads as truncation, as though the reason were there and got
// lost, when in fact herdr said nothing at all.
func TestCmdError_NoDanglingSeparatorOnSilentFailure(t *testing.T) {
	silent := cmdError("workspace list", errors.New("exit status 1"), "")
	if got := silent.Error(); strings.HasSuffix(got, ": ") || strings.HasSuffix(got, ":") {
		t.Errorf("cmdError with empty stderr = %q, want no trailing separator", got)
	}
	if got, want := silent.Error(), "herdr workspace list: exit status 1"; got != want {
		t.Errorf("cmdError = %q, want %q", got, want)
	}

	// Whitespace-only stderr is silence too.
	blank := cmdError("workspace list", errors.New("exit status 1"), "\n  \n")
	if got, want := blank.Error(), "herdr workspace list: exit status 1"; got != want {
		t.Errorf("cmdError with blank stderr = %q, want %q", got, want)
	}

	// And real stderr still reaches the caller, trimmed.
	loud := cmdError("workspace list", errors.New("exit status 1"), "  server not running\n")
	if got, want := loud.Error(), "herdr workspace list: exit status 1: server not running"; got != want {
		t.Errorf("cmdError with stderr = %q, want %q", got, want)
	}
}
