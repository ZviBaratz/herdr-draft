package app

import (
	"errors"
	"strings"
	"testing"
)

// TestTimeoutReasonsRenderAsOneCleanLine is the app half of the prefix
// contract #141 depends on. Each of the three external calls now reports a
// timeout, and each row that shows one has exactly ONE line to show it in,
// where the cells are scarcest -- so a reason arriving with its package's
// own prefix still attached wastes the part the reader needs.
//
// The messages below are literals rather than errors produced by calling
// the real thing, because producing one means sitting through a real
// deadline. The other half is pinned where it can be: internal/linear's
// TestTimeoutMessagesCarryThePrefixesTheRowStrips and internal/clauth's
// TestTheCLITimeoutMessageCarriesThePrefixTheRowStrips assert that the
// packages really do emit these prefixes, and name these functions when
// they fail.
func TestTimeoutReasonsRenderAsOneCleanLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason func(error) string
		msg    string
		want   string
	}{
		{
			name:   "api_key_cmd, on the issue row",
			reason: linearUnavailableReason,
			msg:    "resolve linear api key: api_key_cmd gave no answer within 1m0s",
			want:   "api_key_cmd gave no answer within 1m0s",
		},
		{
			name:   "the assignedIssues fetch, on the issue panel",
			reason: linearRefreshReason,
			msg:    "linear assigned issues: no answer within 30s",
			want:   "no answer within 30s",
		},
		{
			name:   "clauth, on the account row",
			reason: clauthUnavailableReason,
			msg:    "clauth status --json: no answer within 30s",
			want:   "no answer within 30s",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.reason(errors.New(tc.msg))
			if got != tc.want {
				t.Errorf("reason = %q, want %q -- the package prefix should be gone and the budget should not be", got, tc.want)
			}
			if strings.ContainsAny(got, "\n\r") {
				t.Errorf("reason = %q, want one line", got)
			}
		})
	}
}
