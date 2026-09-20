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
)

// AuthVerdict is what one profile's auth_status means for launching on it.
//
// Four values for three words because a fourth case has to go somewhere: a
// non-empty value that is none of clauth's three. By clauth's own evolution
// rule a new value is a breaking change that bumps the schema
// (https://github.com/uwuclxdy/clauth/blob/v0.15.2/wiki/Daemon.md#L165), at
// which point ParseStatus already degrades and AuthOf never looks -- so it
// is unreachable rather than merely unlikely. It is still named, because
// "unreachable" is a claim about clauth's discipline and the row has to draw
// something either way.
type AuthVerdict int

const (
	// AuthUsable is "ok", an absent value, and every profile of a degraded
	// or silent status -- everything nothing is known against.
	AuthUsable AuthVerdict = iota
	// AuthSelfHealing is AuthExpired: worth marking, never worth refusing.
	AuthSelfHealing
	// AuthDead is AuthBroken, the only verdict that refuses a launch.
	AuthDead
	// AuthUnrecognized is a value none of the three. It is marked like
	// AuthDead and refuses like AuthUsable: the defect this vocabulary
	// exists to fix was over-refusal, and a block whose remedy nobody can
	// state leaves a session uncreatable, where a launch on a bad
	// credential has the agent saying so within seconds.
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
	case "", AuthOK:
		return AuthUsable
	case AuthExpired:
		return AuthSelfHealing
	case AuthBroken:
		return AuthDead
	}
	return AuthUnrecognized
}
