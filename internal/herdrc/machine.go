package herdrc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// MachineInfo is one saved herdr machine, as `herdr machine list --json`
// prints it (herdr 0.9.1; machine spec §3.4). ID is what this package hands
// herdr's `--machine`, always: a label can be renamed or shared, and herdr
// refuses an ambiguous one.
type MachineInfo struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Target   string `json:"target"`
	Session  string `json:"session"`
	Enabled  bool   `json:"enabled"`
	Selected bool   `json:"selected"`
}

// argv is args as this runner sends them: behind `--machine <id>` when the
// runner is scoped to a machine. It is the one place the prefix is added,
// and every exec site goes through it -- runJSON, runOK, runText and
// pollDetection's own `agent get` -- because a single unprefixed call on a
// remote session would act on the LOCAL server, with an id that names
// something else there (machine spec §8.1). herdr routes the prefix to the
// saved profile's ssh target and session
// (https://github.com/herdrdev/herdr/blob/v0.9.1/src/cli/target.rs).
func (r *CLIRunner) argv(args ...string) []string {
	if r.Machine == "" {
		return args
	}
	return append([]string{"--machine", r.Machine}, args...)
}

// On returns a copy of r that sends every command to the saved machine id,
// or to this machine when id is "". A copy, never r itself: the popup and
// create each hold one runner for their local calls, and scoping it in
// place would send the next of those to the machine.
func (r *CLIRunner) On(id string) Runner {
	scoped := *r
	scoped.Machine = id
	return &scoped
}

// MachineList runs `herdr machine list --json`: the saved machines, read
// from this machine's own client state (no network, about 3ms). It is never
// prefixed, whatever r is scoped to -- herdr does not forward `machine`
// (validate_machine_command, src/cli/target.rs at v0.9.1) -- and its output
// is a bare JSON array, not the `{"result": ...}` envelope runJSON parses:
// the third of herdr's success shapes this package meets, after runJSON's
// and runOK's.
func (r *CLIRunner) MachineList(ctx context.Context) ([]MachineInfo, error) {
	args := []string{"machine", "list", "--json"}
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, cmdError(strings.Join(args, " "), err, stderr.String())
	}
	var machines []MachineInfo
	if err := json.Unmarshal(stdout.Bytes(), &machines); err != nil {
		return nil, fmt.Errorf("herdr machine list --json: parse response: %w", err)
	}
	return machines, nil
}

// NotificationShow runs `herdr notification show <title> [--body <body>]`,
// herdr's own toast. It prints an envelope (print_response,
// herdr:src/cli/notification.rs at v0.9.0), so it is a runJSON call.
func (r *CLIRunner) NotificationShow(ctx context.Context, title, body string) error {
	args := []string{"notification", "show", title}
	if body != "" {
		var err error
		if args, err = appendFlag(args, "--body", body); err != nil {
			return err
		}
	}
	_, err := r.runJSON(ctx, args...)
	return err
}

// plainStderr is stderr as a person should read it. herdr reports a failure
// to reach a machine not as an envelope but as the Rust Debug of an
// io::Error -- `Error: Custom { kind: Unsupported, error: "machine 'dev
// (EC2)': remote Herdr does not support machine API forwarding; ..." }`
// (machine spec §3.4) -- and the message inside is all that means
// anything. Display only: classification reads cliError.code, which this
// never touches (#144). Anything not in exactly that shape, or whose
// escaping Go's unquoting does not share with Rust's, is returned as it
// came.
func plainStderr(stderr string) string {
	s := strings.TrimSpace(stderr)
	const prefix, open = "Error: Custom { kind: ", `error: "`
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, `" }`) {
		return stderr
	}
	i := strings.Index(s, open)
	if i < 0 {
		return stderr
	}
	msg, err := strconv.Unquote(s[i+len(open)-1 : len(s)-2])
	if err != nil {
		return stderr
	}
	return msg
}

// Reason is err as one sentence for a person: herdr's own stderr when err is
// a herdr failure that said something, unwrapped from the Debug form
// plainStderr strips, and err's whole text otherwise. For a row that has no
// room for the argv in front of the reason -- which on a machine-scoped call
// opens with a 32-hex profile id. Display only, like plainStderr.
func Reason(err error) string {
	var e *cliError
	if errors.As(err, &e) {
		if s := strings.TrimSpace(plainStderr(e.stderr)); s != "" {
			return s
		}
	}
	return err.Error()
}
