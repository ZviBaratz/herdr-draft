package clauth

import (
	"bytes"
	"path/filepath"
	"testing"
)

// verdictName is test-local rather than an AuthVerdict.String() in the
// package: nothing in production formats a verdict, and a String() method
// with no caller is the kind of surface `just unused` cannot see (an
// internal/ package is importable as far as staticcheck's non-whole-program
// `unused` is concerned) and so would never be reported as dead.
func verdictName(v AuthVerdict) string {
	switch v {
	case AuthUsable:
		return "AuthUsable"
	case AuthSelfHealing:
		return "AuthSelfHealing"
	case AuthDead:
		return "AuthDead"
	case AuthUnrecognized:
		return "AuthUnrecognized"
	}
	return "AuthVerdict(?)"
}

// TestAuthOf is clauth's whole auth_status vocabulary in one table, and the
// two halves of #243 and #245 crossed against each other.
//
// The values are closed at v0.15.2 -- `ok`, `expired`, `broken`, and absent
// meaning `ok` -- so `revoked` stands for the case clauth's own evolution
// rule says cannot arrive without a schema bump. It is Unrecognized rather
// than Dead because only `broken` refuses (#243), and it is not Usable
// either: the row still marks it.
func TestAuthOf(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  AuthVerdict
	}{
		{"absent means ok", "", AuthUsable},
		{"ok", AuthOK, AuthUsable},
		{"expired refreshes itself", AuthExpired, AuthSelfHealing},
		{"broken is the dead one", AuthBroken, AuthDead},
		{"a value clauth has never written", "revoked", AuthUnrecognized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := Status{Profiles: []Profile{{Name: "alpha", AuthStatus: tc.value}}}
			if got := st.AuthOf("alpha"); got != tc.want {
				t.Errorf("AuthOf(%q) = %s, want %s", tc.value, verdictName(got), verdictName(tc.want))
			}
			// #245: the SAME status, marked degraded, reports every
			// profile usable. A degraded parse recovered the field but
			// not its meaning, and refusing on a field the row will not
			// even show is the defect that issue names.
			st.Degraded = true
			if got := st.AuthOf("alpha"); got != AuthUsable {
				t.Errorf("degraded AuthOf(%q) = %s, want AuthUsable", tc.value, verdictName(got))
			}
		})
	}
}

// TestAuthOfNamesClauthDoesNotList pins the other half of the lookup: a pin
// naming a profile the current feed has no entry for is not judged, because
// there is nothing to judge it against.
func TestAuthOfNamesClauthDoesNotList(t *testing.T) {
	st := Status{Profiles: []Profile{{Name: "alpha", AuthStatus: AuthBroken}}}
	for _, name := range []string{"", "beta", "ALPHA"} {
		if got := st.AuthOf(name); got != AuthUsable {
			t.Errorf("AuthOf(%q) = %s, want AuthUsable", name, verdictName(got))
		}
	}
}

// TestAuthOfBrokenSurvivesAParse runs the one word that now carries the
// entire refusal through ParseStatus rather than a struct literal. Before
// this, `broken` appeared nowhere in this tree but clauth's own source: both
// testdata fixtures carry only `ok` and `expired`, so a typo in the constant
// would have been invisible to every test.
func TestAuthOfBrokenSurvivesAParse(t *testing.T) {
	raw := readFixture(t, filepath.Join("testdata", "status_schema2.json"))
	mutated := bytes.Replace(raw, []byte(`"auth_status": "expired"`), []byte(`"auth_status": "broken"`), 1)
	if bytes.Equal(mutated, raw) {
		t.Fatal("fixture does not carry alpha's expired auth_status; test setup is broken")
	}
	st, err := ParseStatus(mutated)
	if err != nil {
		t.Fatalf("ParseStatus: %v", err)
	}
	if st.Degraded {
		t.Fatal("Degraded = true; the fixture's schema is one this package knows")
	}
	if got := st.AuthOf("alpha"); got != AuthDead {
		t.Errorf("AuthOf(alpha) = %s, want AuthDead", verdictName(got))
	}
	if got := st.AuthOf("beta"); got != AuthUsable {
		t.Errorf("AuthOf(beta) = %s, want AuthUsable", verdictName(got))
	}
}
