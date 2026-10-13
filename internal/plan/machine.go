package plan

import (
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
)

// Machine is the herdr machine a session runs on (machine spec §8.1). Its
// zero value is this machine, which is every plan built before the field
// existed.
//
// It lives on Input, and Build stamps it on every Op, rather than in
// ExecOpts: ExecOpts holds only what differs between the popup and create
// at run time (CLAUDE.md), and the machine is part of WHAT is built -- the
// same choice must produce the same ops from both, which
// equivalence_test.go asserts. Execute, CleanCheck and Clean read it and
// scope herdr and git themselves, so no caller can hand a remote plan a
// local runner or the laptop's clone.
type Machine struct {
	// ID is the saved herdr machine's profile id, what `herdr --machine`
	// is handed; "" is this machine.
	ID string
	// Label is the profile's label, for messages: a step that failed on
	// another machine has to say so, since its pane is on a screen the
	// laptop is not showing.
	Label string
	// Target is the profile's ssh target, which git on the remote clone
	// goes over (gitx.Over).
	Target string
	// Dir is the project's absolute directory on that machine, as its
	// clone probe reported it: `worktree create`'s and `workspace create`'s
	// --cwd there, and where the clean gate's git runs.
	Dir string
}

// Remote reports whether m is another machine.
func (m Machine) Remote() bool { return m.ID != "" }

// Git is the machine m's git: this machine's for the zero value, and
// `ssh <Target>` for another.
func (m Machine) Git() gitx.Host {
	if !m.Remote() {
		return gitx.Here
	}
	return gitx.Over(m.Target)
}

// remoteFetchTimeout bounds the fetch on another machine's clone before a
// worktree is cut there (machine spec §8.3): the size of the popup's own
// fetchPruneTimeout, for the same job on a clone further away. A constant,
// not a [timeouts] key, for that one's reason -- a safety bound, not a
// tuning knob.
const remoteFetchTimeout = 30 * time.Second
