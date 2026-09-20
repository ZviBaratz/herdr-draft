package clauth

// The auth_status values clauth writes, and the whole set of them: clauth
// documents the field as `"ok" | "expired" | "broken"`, with an absent field
// reading as "ok"
// (https://github.com/uwuclxdy/clauth/blob/v0.15.2/wiki/Daemon.md#L150 and
// #L165's reader rule). They are constants rather than literals at each
// reader because knownSchemas' admission ritual now cites them: a schema
// bump has to be checked against these VALUES, not only against the field
// set (#243).
const (
	// AuthOK is a credential clauth has nothing to say about.
	AuthOK = "ok"
	// AuthExpired is an OAuth access token past its expiry whose refresh
	// has not run yet. It is NOT a dead credential and `clauth login` is
	// not its remedy: clauth's own pre-install gate refreshes one
	// (`AuthGate::Refreshed`), and the command this plugin actually types
	// for a pinned launch -- `clauth start <profile> --` -- runs no gate
	// at all, handing the stored file's access token AND refresh token to
	// `claude`, which refreshes it itself
	// (https://github.com/uwuclxdy/clauth/blob/v0.15.2/src/claude.rs:
	// "`clauth start` resolves its credentials through
	// `install_source_path`, never `ensure_installable`").
	AuthExpired = "expired"
	// AuthBroken is a credential whose last refresh was rejected as
	// revoked or invalid. It is the value that means "cannot be used at
	// all", it outranks AuthExpired, and clauth itself excludes it from
	// fallback walks and refuses it as a switch target.
	AuthBroken = "broken"
	// AuthExpiring is schema 1's spelling of AuthExpired, and the reason
	// this package translates rather than only recognising: schema 2's one
	// breaking change was that rename, and clauth's own note on it says a
	// reader keying on the new word "must refuse or translate"
	// (https://github.com/uwuclxdy/clauth/blob/v0.15.2/src/daemon/status_json.rs#L33-L36).
	// knownSchemas admits schema 1 in full, so herdr-draft neither refuses
	// it nor may pretend the word is unfamiliar -- and schema 1 is not
	// history: it is what every clauth from this plugin's own documented
	// floor up to 0.14.1 writes
	// (https://github.com/uwuclxdy/clauth/blob/v0.14.1/src/daemon/status_json.rs#L100).
	AuthExpiring = "expiring"
	// AuthUnknown is "clauth has no reading for this account", not a
	// finding about its credential: a codex profile (`harness: "codex"`)
	// publishes it when no usage cache exists for it yet, alongside
	// `broken` for a quarantined chain and `ok` once a cache lands
	// (https://github.com/uwuclxdy/clauth/blob/3b10a4e79063daecd905d2ae361c122a8c25a678/src/daemon/status_json.rs#L588-L593).
	// So it reads exactly like an absent value: nothing is wrong, and
	// nothing is known.
	//
	// It is also the counterexample that this package's rules are written
	// around. It arrived ADDITIVELY under schema 2, named as such in
	// clauth's own evolution rule, which is a rule about fields -- "new
	// fields may appear, existing fields never change type/meaning" -- and
	// has never promised that the set of VALUES is closed under a schema.
	// Anything here that reasons "a new value would bump the schema, so
	// ParseStatus would have degraded it" is reasoning from a rule that
	// does not exist.
	AuthUnknown = "unknown"
)

// AuthVerdict is what one profile's auth_status means for launching on it.
//
// Four verdicts over five words, plus a case for the sixth nobody has seen.
// That last case is REACHED, not theoretical, and the constants above are
// why: `unknown` joined the set additively under schema 2, and `expiring`
// was the same state under a different name one schema earlier. Both would
// have arrived here as "a word I do not know" on a status ParseStatus was
// perfectly happy with.
//
// So the unrecognised arm is drawn as a warning in clauth's own words
// rather than as a failure. Two additions, neither of them a dead
// credential, are the whole sample: a reader that paints an unfamiliar word
// red is wrong more often than it is right, and it is wrong in the
// direction this package exists to stop being wrong in.
type AuthVerdict int

const (
	// AuthUsable is "ok", an absent value, and every profile of a degraded
	// or silent status -- everything nothing is known against.
	AuthUsable AuthVerdict = iota
	// AuthSelfHealing is AuthExpired: worth marking, never worth refusing.
	AuthSelfHealing
	// AuthDead is AuthBroken, the only verdict that refuses a launch.
	AuthDead
	// AuthUnrecognized is a value none of the five. It refuses nothing --
	// the defect this vocabulary exists to fix was over-refusal, and a
	// block whose remedy nobody can state leaves a session uncreatable,
	// where a launch on a bad credential has the agent saying so within
	// seconds -- and it is SHOWN, in the warning colour, spelled the way
	// clauth spelled it, so a reader can go and look it up.
	AuthUnrecognized
)

// AuthOf reports what s says about the profile called name.
//
// It is a method on Status, and the profile-level classifier below is
// deliberately unexported, because BOTH of this function's guards are ones
// a caller iterating Profiles itself would forget -- which is exactly how
// #245 shipped, with two of the four readers consulting AuthStatus on a
// status whose every field past Name the package's own doc calls unreliable,
// while the row that would have shown it drew names only.
//
//   - A Degraded status answers AuthUsable for everything. Degraded means
//     the schema is one nobody has read clauth's reason for, so a value that
//     still spells `broken` may no longer mean it. Refusing on it is a guess,
//     and refusing on a field the popup will not display is a guess the user
//     cannot see, let alone answer.
//   - A name s does not list answers AuthUsable. There is nothing to judge
//     it against: a pin can outlive the profile it names (a reload between
//     the pin and the submit), and clauth hides disabled accounts from
//     profiles[] entirely.
//
// The alternative considered and rejected was making the bad state
// unrepresentable -- having ParseStatus zero every field past Name on the
// degraded full-parse path, as the minimal fallback already does. Status's
// own Degraded doc promises the opposite ("still carries whatever the full
// parse recovered"), and that promise is #238's entire rationale for reading
// schema 2 in full rather than as names.
func (s Status) AuthOf(name string) AuthVerdict {
	if s.Degraded || name == "" {
		return AuthUsable
	}
	for _, p := range s.Profiles {
		if p.Name == name {
			return authOf(p.AuthStatus)
		}
	}
	return AuthUsable
}

// authOf classifies one auth_status value. Exact, never case-folded or
// trimmed: clauth emits these from a `&'static str` match, so a differently
// cased spelling is a different writer than the one this package was read
// against, and AuthUnrecognized is the honest answer for it.
func authOf(status string) AuthVerdict {
	switch status {
	case "", AuthOK, AuthUnknown:
		return AuthUsable
	case AuthExpired, AuthExpiring:
		return AuthSelfHealing
	case AuthBroken:
		return AuthDead
	}
	return AuthUnrecognized
}
