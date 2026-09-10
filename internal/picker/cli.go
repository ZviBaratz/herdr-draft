package picker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Options are the protocol's two optional flags.
type Options struct {
	// Strict asks for headless semantics -- no interactive fallback, no
	// waiting for a person. `create` sets it; the popup does not.
	Strict bool
	// DryRun asks the picker not to write its ledger and not to build an
	// account directory, so config_dir comes back null. The popup's live
	// preview sets it; the commit-time pick must not, or two sessions opened
	// a second apart would both be handed the same account.
	DryRun bool
}

// Source is the picker access herdr-draft depends on, so internal/app and
// internal/create can be tested against a fake and never run a subprocess.
type Source interface {
	Pick(ctx context.Context, dir string, opts Options) (Result, error)
}

// Argv is the documented invocation, exported because it is the contract's
// most quotable half and the README and the tests should read one definition
// of it.
func Argv(bin, dir string, opts Options) []string {
	argv := []string{bin, "--dir", dir, "--json"}
	if opts.Strict {
		argv = append(argv, "--strict")
	}
	if opts.DryRun {
		argv = append(argv, "--dry-run")
	}
	return argv
}

// CLI runs a picker executable. Bin is what `[clauth] picker` named -- a
// command name resolved on PATH, or an absolute path.
//
// There is deliberately no "find a picker on PATH" constructor. Finding *a*
// program called claude-pick is not finding *this* one, and a plugin that
// silently adopted a same-named stranger to route account credentials would
// be worse than a plugin with no picker at all. Bin comes from config, and
// Probe checks it before anything trusts it.
type CLI struct{ Bin string }

var _ Source = CLI{}

// Pick runs one pick and returns its answer.
//
// Three outcomes, kept distinct because callers act differently on each: a
// Result; a *RefusalError (the picker ran and said no -- a decision); or a
// plain error (the picker could not be run, or answered outside its own
// protocol -- a malfunction).
func (c CLI) Pick(ctx context.Context, dir string, opts Options) (Result, error) {
	code, stdout, stderr, err := c.run(ctx, dir, opts)
	if err != nil {
		return Result{}, err
	}

	// Decode FIRST, even on a refusal: the protocol reports its `reason` in
	// the JSON body on every exit path, and that sentence is more useful than
	// anything this package could compose from an exit code.
	res, derr := Decode(stdout)

	if code != ExitPicked {
		reason := res.Reason
		if derr != nil || strings.TrimSpace(reason) == "" {
			reason = strings.TrimSpace(string(stderr))
		}
		return Result{}, &RefusalError{Code: code, Reason: reason}
	}
	if derr != nil {
		return Result{}, fmt.Errorf("picker %s: %w", c.Bin, derr)
	}
	if res.Profile == "" {
		// Exit 0 means picked. A picker that exits 0 with no profile has
		// broken its own contract, and passing the empty string on as a pin
		// would launch under whatever account happened to be live while the
		// row claimed the picker had chosen one.
		return Result{}, fmt.Errorf("picker %s: exit 0 (picked) but no profile in the answer", c.Bin)
	}
	return res, nil
}

// Probe runs ONE `--json --dry-run` against dir and reports whether the
// executable behaves like a picker: an exit code from the documented set, and
// an answer carrying the documented keys.
//
// It judges the SHAPE of the answer, never the answer. A machine whose pool
// is exhausted, or which has no tenant table configured, still has a
// perfectly good picker -- refusing it there would turn the feature off on
// exactly the day the user most needs to see why it said no.
//
// --dry-run is not incidental: a probe must not write a ledger entry or build
// an account directory for a session nobody has asked for yet.
func (c CLI) Probe(ctx context.Context, dir string) error {
	code, stdout, stderr, err := c.run(ctx, dir, Options{DryRun: true})
	if err != nil {
		return err
	}
	switch code {
	case ExitPicked, ExitExhausted, ExitBackpressure, ExitBadTable, ExitNoProfiles, ExitUsage:
	default:
		note := strings.TrimSpace(string(stderr))
		if note != "" {
			note = ": " + flatten(note)
		}
		return fmt.Errorf("%s exited %d, which is not one of the protocol's codes (0, 2, 3, 4, 5, 64)%s", c.Bin, code, note)
	}
	if missing := MissingKeys(stdout); len(missing) > 0 {
		return fmt.Errorf("%s answered without the documented keys %s -- it does not implement the account-picker protocol",
			c.Bin, strings.Join(missing, ", "))
	}
	return nil
}

// run executes the picker and separates "did not run" from "ran and exited
// non-zero". Only the latter carries an exit code worth interpreting.
func (c CLI) run(ctx context.Context, dir string, opts Options) (code int, stdout, stderr []byte, err error) {
	if c.Bin == "" {
		return 0, nil, nil, errors.New("picker: no executable configured")
	}
	argv := Argv(c.Bin, dir, opts)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		return 0, out.Bytes(), errBuf.Bytes(), nil
	case errors.As(runErr, &exitErr):
		return exitErr.ExitCode(), out.Bytes(), errBuf.Bytes(), nil
	default:
		// Could not start: not on PATH, not executable, context cancelled.
		// Never a refusal -- nothing refused anything.
		if note := strings.TrimSpace(errBuf.String()); note != "" {
			return 0, nil, nil, fmt.Errorf("picker %s: %w: %s", c.Bin, runErr, flatten(note))
		}
		return 0, nil, nil, fmt.Errorf("picker %s: %w", c.Bin, runErr)
	}
}
