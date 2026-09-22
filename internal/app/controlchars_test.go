package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
)

// TestRenderPromptTemplate_KeepsNoControlCharacter pins #183's rule where
// both paths get the Linear-seeded prompt from: the escape sequences in an
// issue's title and description are gone before either path sees the
// text, and its line breaks are not. The popup's textarea would drop them
// anyway; `create` has nothing else between this and herdr.
func TestRenderPromptTemplate_KeepsNoControlCharacter(t *testing.T) {
	got := RenderPromptTemplate("", linear.Issue{
		Identifier:  "ENG-1",
		Title:       "Fix login\x1b[201~",
		URL:         "https://linear.app/x/ENG-1",
		Description: "it loops\x1b[Z\r\nforever\a",
	})
	want := "Work on ENG-1: Fix login[201~\n\nhttps://linear.app/x/ENG-1\n\nit loops[Z\nforever"
	if got != want {
		t.Errorf("RenderPromptTemplate = %q, want %q", got, want)
	}
}

// TestReasonsAreDrawnWithoutControlCharacters is #151's app half. Every
// reason another program gives -- Linear, clauth, the account picker --
// reaches a row through one of these, and strings.Fields, which each of
// them already used to put the reason on one line, splits on a CR but
// keeps an ESC. The picker's refusal is the case that went round the
// others: picker.RefusalError.Short flattens with a copy of its own.
func TestReasonsAreDrawnWithoutControlCharacters(t *testing.T) {
	const raw = "no key\x1b[2J here\r\n\tbecause\a"
	const want = "no key[2J here because"
	for _, tc := range []struct {
		name   string
		reason func() string
	}{
		{"flattenReason", func() string { return flattenReason(raw) }},
		{"linearUnavailableReason, on the issue row", func() string {
			return linearUnavailableReason(errors.New("resolve linear api key: " + raw))
		}},
		{"linearRefreshReason, on the issue panel", func() string {
			return linearRefreshReason(errors.New("linear assigned issues: " + raw))
		}},
		{"clauthUnavailableReason, on the account row", func() string {
			return clauthUnavailableReason(errors.New("clauth status --json: " + raw))
		}},
		{"a picker that could not run, on the auto row", func() string {
			return previewFrom(picker.Result{}, errors.New(raw)).Refusal
		}},
		{"a picker's refusal, on the auto row", func() string {
			return previewFrom(picker.Result{}, &picker.RefusalError{Code: 3, Reason: raw}).Refusal
		}},
		{"a picker's warning, on the account panel", func() string {
			return strings.Join(previewFrom(picker.Result{Profile: "alpha-1", Warnings: []string{raw}}, nil).Warnings, "|")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.reason(); got != want {
				t.Errorf("reason = %q, want %q", got, want)
			}
		})
	}
}
