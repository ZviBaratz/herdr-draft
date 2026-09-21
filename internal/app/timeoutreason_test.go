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
			// #323: runKeyCmd puts the command's stderr in the error, and
			// a credential helper's stderr can run to several lines.
			name:   "api_key_cmd's multi-line stderr, on the issue row",
			reason: linearUnavailableReason,
			msg:    "resolve linear api key: run api_key_cmd: exit status 1: [ERROR] 2026/09/21 not signed in\n  run `op signin`\r\n\tto continue",
			want:   "run api_key_cmd: exit status 1: [ERROR] 2026/09/21 not signed in run `op signin` to continue",
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

// A clauth reload that TIMES OUT puts its budget on the account row.
//
// This is the one popup surface #141 did not create for itself. Bootstrap's
// open-time read already degraded the row with whatever clauth's error
// said, and before the bound that error never arrived; what #255 added,
// landing while this was in flight, is that focusing an unavailable row
// asks clauth again and the ANSWER replaces the reason. The two compose
// without either side knowing about the other -- but "compose" is a claim,
// and this is the check of it. A live row is deliberately left alone by a
// failed reload, which is #255's own decision and is pinned beside its
// sibling in accountreload_test.go.
func TestClauthReload_ATimedOutReloadNamesItsBudget(t *testing.T) {
	ranOut := errors.New("clauth status --json: no answer within 30s")
	m := resolveDirCheck(t, newTestModel(t, unavailableAccountSetup(&fakeClauth{err: ranOut})))
	m, res := clickAccountRow(t, m)
	next, _ := m.Update(res)
	m = next.(Model)

	row := accountRow(t, m)
	if !strings.Contains(row, "no answer within 30s") {
		t.Errorf("the account row = %q, want the budget the reload ran out of", row)
	}
	if strings.Contains(row, "clauth status --json") {
		t.Errorf("the account row = %q, want the package prefix stripped", row)
	}
	if m.account.Enabled() {
		t.Error("the account row went live on a reload that never answered")
	}
}
