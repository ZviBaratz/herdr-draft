// Package linear reads the caller's assigned Linear issues over Linear's
// GraphQL API for the herdr-draft form's issue picker (spec §10). It is
// deliberately read-only: no comments, no status writes -- the GitHub
// integration already owns issue status transitions, and manual writes
// from here would fight it.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// assignedIssuesQuery is the Linear GraphQL query for the personal
// "assigned issues" list the form uses to seed a new session, copied
// verbatim from spec §10 (docs/specs/2026-08-31-herdr-draft-design.md,
// "Linear integration (read-only)" -> Query block) so the wire query this
// client sends can be diffed against the design doc directly.
const assignedIssuesQuery = `  { viewer { assignedIssues(
      filter: { state: { type: { in: ["unstarted", "started"] } } },
      orderBy: updatedAt, first: 50
    ) { nodes {
        identifier title branchName url description
        state { name type } estimate priority cycle { number }
  } } } }`

// defaultEndpoint is Linear's public GraphQL API endpoint, used when
// Client.Endpoint is empty.
//
// A var and not a const for ONE reason: so `just live` can link a copy of
// the binary whose issue query goes to hack/live's stub instead (#302),
// with `-ldflags -X .../internal/linear.defaultEndpoint=http://127.0.0.1:N/...`.
// Nothing assigns it at runtime, and nothing may: that would be a setting,
// and a setting that decides where the user's Linear key is sent is the
// thing #302 chose not to add. The linker is the only way in, so a binary
// is redirected only if whoever BUILT it asked for that -- and
// herdr-plugin.toml's [[build]], which is what builds every installed
// copy, never does. TestNoInstalledBinaryCanBeRedirected reads the real
// manifest to keep it that way, and TestClientDefaultEndpoint pins the
// value that build gets.
var defaultEndpoint = "https://api.linear.app/graphql"

// Issue is one Linear issue returned by AssignedIssues, shaped for the form
// fields described in spec §10 (issue picker + seeding template). Estimate
// and CycleNumber are pointers because Linear reports both as JSON null
// when unset (verified against a "started" issue carrying no cycle) --
// nil means "not reported", distinct from a real zero value.
type Issue struct {
	Identifier  string
	Title       string
	BranchName  string
	URL         string
	Description string
	StateName   string
	StateType   string
	Estimate    *float64
	Priority    int
	CycleNumber *int
}

// Client talks to the Linear GraphQL API on behalf of the herdr-draft form.
// It performs only the read-only assignedIssues query; see the package doc
// for why writes are out of scope.
type Client struct {
	HTTP     *http.Client
	APIKey   string
	Endpoint string
}

// httpClient returns c.HTTP, defaulting to http.DefaultClient when unset.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// endpoint returns c.Endpoint, defaulting to defaultEndpoint when empty.
func (c *Client) endpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	return defaultEndpoint
}

// graphQLRequest is the wire body of a GraphQL POST request.
type graphQLRequest struct {
	Query string `json:"query"`
}

// graphQLError is one entry in a GraphQL response's top-level "errors"
// array -- a server-side rejection of the query (bad auth, malformed
// query), distinct from an HTTP transport failure.
type graphQLError struct {
	Message string `json:"message"`
}

// assignedIssuesResponse mirrors Linear's response shape for
// assignedIssuesQuery: {"data":{"viewer":{"assignedIssues":{"nodes":[...]}}}}.
// Data is a pointer because a request that only fails at the GraphQL level
// (Errors non-empty) can carry a null/absent "data" key.
type assignedIssuesResponse struct {
	Data *struct {
		Viewer struct {
			AssignedIssues struct {
				Nodes []issueNode `json:"nodes"`
			} `json:"assignedIssues"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

// issueNode is one element of assignedIssuesResponse's nodes[], matching
// the field selection in assignedIssuesQuery exactly. Estimate and Cycle
// are pointers to preserve Linear's JSON null for "not set".
type issueNode struct {
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	BranchName  string `json:"branchName"`
	URL         string `json:"url"`
	Description string `json:"description"`
	State       struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Estimate *float64 `json:"estimate"`
	Priority int      `json:"priority"`
	Cycle    *struct {
		Number int `json:"number"`
	} `json:"cycle"`
}

// httpTimeout bounds one assignedIssues exchange (#141). Before it there was
// no bound at all: httpClient() defaults to http.DefaultClient, whose
// Timeout is zero, so a proxy that accepted the connection and never
// answered left `create --issue` with no output and no exit code, and the
// popup's background refresh waiting for the life of the process.
//
// THIRTY seconds, the number this repository already uses for "slow but not
// hung" -- app.fetchPruneTimeout, picker.pickerTimeout,
// create.preflightCheckDeadline. Unlike api_key_cmd beside it, nothing here
// can be waiting on a person: this is one GraphQL POST to a public API, and
// a Linear that has not answered in thirty seconds is not about to.
//
// A constant rather than a [timeouts] key, for keyCmdTimeout's reason.
//
// A var, not a const, only so the tests can shrink it.
var httpTimeout = 30 * time.Second

// assignedIssuesPrefix is this package's own error prefix for the fetch,
// which app.linearRefreshReason strips before putting the reason on the
// issue panel's status row.
const assignedIssuesPrefix = "linear assigned issues"

// wireError classifies a failed exchange. The DEADLINE is read from the
// CONTEXT rather than from the error's chain, and the reason is uniformity
// with this change's other two classifiers rather than necessity here.
//
// The chain can answer, which an earlier version of this comment denied.
// Probed on both sides: a *url.Error from an expired request matches
// context.DeadlineExceeded and not Canceled, one from a cancelled request
// matches Canceled and not DeadlineExceeded, and io.ReadAll on a stalled
// body returns context.deadlineExceededError directly. So `errors.Is` on
// the returned error would work at both failure points, and the second
// version of this comment's reason -- that only the context gives one
// answer for both -- does not hold either.
//
// What does hold: runKeyCmd and clauth's loadFromCLI CANNOT read the chain
// in the shape that dominates. A subprocess killed WHILE RUNNING reports
// `signal: killed` -- an *exec.ExitError, which has no Unwrap, so
// errors.Is(err, context.DeadlineExceeded) is false on it and a chain-only
// classifier would call a timeout an ordinary failure. They must ask the
// context, so this asks the context too, and the three read the same way.
//
// The other shape does carry it, and naming that here is the point: a
// child that exits 0 before Process.Wait can reap it comes back as the
// CONTEXT error itself (classifyRun has the mechanism and the sweep).
// Every version of this sentence in this change -- here, in runKeyCmd and
// in classifyRun -- was first written as a universal about killed
// subprocesses, and the zombie shape went unnamed in all of them. It
// changes nothing about which side to read, since the dominant shape is
// the one that cannot answer; it changes what the reader is told is
// always true. The one place the two differ is
// worth knowing rather than hiding: a request that failed for an unrelated
// reason while the deadline happened to be expiring is reported here as a
// timeout, where the chain would have named the connection error. That is
// the same trade picker.CLI.run makes, and at a thirty-second budget the
// coincidence is not one to design around.
//
// A caller that was cancelled is not a call that timed out (#272), so
// DeadlineExceeded specifically, never `ctx.Err() != nil`.
//
// Reading the context carries runKeyCmd's caveat, which applies here too: a
// caller whose own context held a deadline SHORTER than httpTimeout would
// be told "no answer within 30s" about a budget that never fired. Neither
// caller does -- app's refresh passes context.Background and create's
// pre-flight context carries a cancel and no deadline -- and this package
// cannot be imported from outside the module.
func wireError(ctx context.Context, stage string, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return timeout{prefix: assignedIssuesPrefix, what: "no answer", deadline: httpTimeout}
	}
	return fmt.Errorf("%s: %s: %w", assignedIssuesPrefix, stage, err)
}

// AssignedIssues runs assignedIssuesQuery against c.Endpoint and returns the
// caller's currently assigned, not-yet-done issues (spec §10: state type in
// ["unstarted", "started"], ordered by updatedAt, capped at 50). It never
// mutates Linear state.
func (c *Client) AssignedIssues(ctx context.Context) ([]Issue, error) {
	body, err := json.Marshal(graphQLRequest{Query: assignedIssuesQuery})
	if err != nil {
		return nil, fmt.Errorf("linear assigned issues: encode request: %w", err)
	}

	// The deadline rides on the REQUEST, and http.Client.Timeout is
	// deliberately not set as well. The request context already "controls the
	// entire lifetime of a request and its response: obtaining a connection,
	// sending the request, and reading the response headers and body"
	// (go1.26.4 src/net/http/request.go, NewRequestWithContext), so it covers
	// the io.ReadAll below too. Setting both would put two timers on one
	// deadline, and which fired first would decide whether the caller saw
	// this package's clause or net/http's "Client.Timeout exceeded while
	// awaiting headers" -- the shape #272's review caught, arriving by
	// another door. Riding on the request is also what stops an injected
	// Client.HTTP from escaping the bound.
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("linear assigned issues: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Linear personal API keys go in Authorization with no "Bearer " prefix.
	req.Header.Set("Authorization", c.APIKey)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, wireError(ctx, "request", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wireError(ctx, "read response", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("linear assigned issues: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed assignedIssuesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("linear assigned issues: parse response: %w", err)
	}
	if len(parsed.Errors) > 0 {
		msgs := make([]string, len(parsed.Errors))
		for i, e := range parsed.Errors {
			msgs[i] = e.Message
		}
		return nil, fmt.Errorf("linear assigned issues: %s", strings.Join(msgs, "; "))
	}
	if parsed.Data == nil {
		return nil, fmt.Errorf("linear assigned issues: response had no data and no errors")
	}

	nodes := parsed.Data.Viewer.AssignedIssues.Nodes
	issues := make([]Issue, len(nodes))
	for i, n := range nodes {
		issue := Issue{
			Identifier:  n.Identifier,
			Title:       n.Title,
			BranchName:  n.BranchName,
			URL:         n.URL,
			Description: n.Description,
			StateName:   n.State.Name,
			StateType:   n.State.Type,
			Estimate:    n.Estimate,
			Priority:    n.Priority,
		}
		if n.Cycle != nil {
			num := n.Cycle.Number
			issue.CycleNumber = &num
		}
		issues[i] = issue
	}
	return issues, nil
}

// ErrTimeout is what a Linear call that did not answer inside its budget
// reports through, so a caller that has to branch can find it with a single
// errors.Is through however the call site wrapped the error.
//
// One sentinel for both budgets below, because the only caller that branches
// -- internal/create's refuse -- makes the same decision for either: a
// question it cannot answer refuses the run.
var ErrTimeout = errors.New("linear: no answer within the deadline")

// timeout is one Linear call that did not answer: the sentence a caller
// reads, and ErrTimeout to the code that has to branch.
//
// A type with its own Is rather than an fmt.Errorf wrap of the sentinel, so
// the message stays one clause instead of ending in the sentinel's own text
// repeating what the clause already said. internal/create's checkTimeout and
// herdrc's cliError do the same.
//
// The clause carries this package's own error prefix, because the app layer
// strips exactly those prefixes before putting a reason on a one-line row
// (app.linearUnavailableReason, app.linearRefreshReason) -- a message without
// one renders raw.
type timeout struct {
	prefix   string
	what     string
	deadline time.Duration
}

func (e timeout) Error() string {
	return fmt.Sprintf("%s: %s within %s", e.prefix, e.what, e.deadline)
}

func (timeout) Is(target error) bool { return target == ErrTimeout }

// configFileName is the plugin config file whose permissions ResolveAPIKey
// checks before trusting an inline api_key literal (spec §12: "discouraged;
// file perms checked (0600)").
const configFileName = "config.toml"

// ResolveAPIKey resolves the Linear personal API key from three possible
// sources, in priority order:
//
//  1. apiKeyCmd (e.g. config's [linear] api_key_cmd, such as
//     ["pass", "show", "linear-api-key"]): if non-empty, it is executed and
//     its trimmed stdout is used. A command that is configured but fails to
//     run is a hard error -- it is not silently treated as absent, since a
//     broken api_key_cmd is a real misconfiguration the user should see.
//     A command that runs successfully but prints nothing is treated the
//     same as "not configured" and resolution falls through to the next
//     source.
//  2. The $LINEAR_API_KEY environment variable.
//  3. apiKeyLiteral (config's [linear] api_key inline value): only trusted
//     when <configDir>/config.toml has no group/other permission bits set
//     (i.e. is no wider than 0600). A wider file is rejected with an error
//     rather than silently falling through, since an over-permissioned
//     config file is the exact hazard this check exists to catch.
//
// When all three sources are absent, ResolveAPIKey returns ("", nil) --
// per spec §10, an absent key means the Linear field is simply not
// rendered, not an error.
//
// ctx bounds source 1 only, and both halves of that are deliberate. The
// command is the part that can hang -- it used to reach plain exec.Command
// and have no deadline at all (#141) -- and runKeyCmd puts keyCmdTimeout on
// top of whatever ctx already carries, so a caller's cancellation still
// reaches it. The permission check on source 3 stays outside: it reads the
// PLUGIN's config directory, which create/checks.go's own grouping of
// unbounded reads classifies as not on the project, and bounding it here
// would make the two comments disagree about the same file.
func ResolveAPIKey(ctx context.Context, apiKeyCmd []string, apiKeyLiteral, configDir string) (string, error) {
	if len(apiKeyCmd) > 0 {
		key, err := runKeyCmd(ctx, apiKeyCmd)
		if err != nil {
			return "", err
		}
		if key != "" {
			return key, nil
		}
	}

	if key := strings.TrimSpace(os.Getenv("LINEAR_API_KEY")); key != "" {
		return key, nil
	}

	if apiKeyLiteral == "" {
		return "", nil
	}

	if err := checkConfigPerm(configDir); err != nil {
		return "", err
	}
	return apiKeyLiteral, nil
}

// keyCmdTimeout bounds api_key_cmd (#141). Without it the command had no
// deadline at all -- not a long one, none: ResolveAPIKey reached plain
// exec.Command, which takes no context.
//
// It is a deadlock breaker rather than a responsiveness budget, and it
// protects the same thing picker.pickerTimeout does, for the same reason:
// app.Bootstrap runs this synchronously BEFORE the popup is drawn, so a
// helper that never answers means the form never appears and never says why.
//
// SIXTY seconds, and deliberately longer than the thirty every other bound
// in this repository uses, because this is the only call herdr-draft makes
// that can legitimately be waiting on a PERSON. The documented shape is
// `pass show ...` or `op read ...`; the second of those raises a desktop
// approval, and answering it means finding the window, unlocking and
// touching a sensor. The number is chosen to sit above the slowest
// legitimate approval rather than near a comfortable one -- cutting a
// working helper off at thirty would report it as broken, which is wrong,
// permanent until someone re-reads the config, and indistinguishable from a
// genuinely broken command.
//
// Both paths take the same number, and the popup's blank pane is the price.
// A shorter budget before the draw would be the failure above; what the
// popup already does while a working helper waits for approval is exactly
// what it does while a hung one does not, so the bound only decides when to
// give up. Drawing the form first and resolving Linear asynchronously is the
// real fix for that and is a change to Bootstrap's shape, not a deadline.
//
// A constant rather than a [timeouts] key, for fetchPruneTimeout's and
// preflightCheckDeadline's reason: a safety bound is not a tuning knob, and
// a key would inherit #143 -- [timeouts] values are not validated at all --
// so one typo would turn this into "give up immediately" and make Linear
// vanish with a reason nobody would connect to the config.
//
// A var, not a const, only so the tests can shrink it -- picker.pickerTimeout's
// shape.
var keyCmdTimeout = 60 * time.Second

// keyCmdWaitDelay is how long cmd.Wait may keep waiting on the command's
// PIPES after the deadline has already killed the command itself.
//
// Measured for the picker and the same here: exec.CommandContext kills the
// process it started, but a grandchild holding the inherited write end of
// the stdout pipe keeps Wait's copying goroutine blocked, so the deadline
// above would bound nothing. A credential helper is exactly that shape --
// `pass` shells out to `gpg`, which talks to a `gpg-agent` it may have
// started -- so this is the ordinary case rather than a contrived one.
// gitx.waitDelay and picker.pickerWaitDelay are the same two seconds for the
// same reason.
var keyCmdWaitDelay = 2 * time.Second

// keyCmdPrefix is this package's own error prefix for key resolution, which
// app.linearUnavailableReason strips before putting the reason on a row.
const keyCmdPrefix = "resolve linear api key"

// runKeyCmd executes api_key_cmd under keyCmdTimeout and returns its trimmed
// stdout. An empty answer is not an error: ResolveAPIKey's contract is that a
// command which runs and prints nothing falls through to the next source.
//
// WHAT THE BOUND IS, stated because the obvious reading is wrong. This is a
// KILL bound, not an answer bound. cmd.Wait calls Process.Wait first and
// unconditionally and only then consults the WaitDelay timer (go1.26.4
// src/os/exec/exec.go, Cmd.Wait), so a child that cannot be reaped -- one in
// uninterruptible sleep on a stalled $HOME -- never returns from cmd.Run and
// this function waits with it. That class is #280's, not #141's: every
// scenario #141 names is a killable wait, and bounding the ANSWER instead
// would mean app.AwaitCheck, which lives in internal/app and which this
// package must not import. picker.CLI.run made the same trade for the same
// shape.
//
// FOUR ARMS, and only the first is about what happened rather than what
// went wrong.
//
// AN ANSWER IN HAND BEATS A CONTEXT THAT IS DONE, which is the same rule
// app.AwaitCheck's own peek states, and it is why this arm is read first
// rather than the deadline. It is the arm that carries the most weight, and
// an earlier version of this comment argued it could not matter. That was
// wrong, and the measurement is the reason to read this rather than infer
// it.
//
// The claim was that a deadline and ErrWaitDelay are mutually exclusive --
// that a run the deadline ended has had its Cancel called, so it could not
// come back as ErrWaitDelay. **They are simultaneously observable, and
// deterministically so.** cmd.Wait takes Process.Wait FIRST, so a command
// that exits 0 before the deadline records no watcher error and no Cancel
// occurs; only then does awaitGoroutines start the WaitDelay timer
// (go1.26.4 src/os/exec/exec.go), and that timer can expire after the
// deadline has passed. The result is runErr == ErrWaitDelay and
// ctx.Err() == DeadlineExceeded both true, with the key already in the
// buffer. Measured at the shipped 60:2 ratio scaled to 1s:33ms: 20 runs out
// of 20, and a deadline sweep around the exit instant hit it about half the
// time. Found in review.
//
// So the order between these arms decides a real outcome, not a
// nanosecond's worth of one: reading the deadline first throws away the key
// of an api_key_cmd that answered between 58s and 60s and had a
// `gpg-agent`-shaped grandchild, and reports it as a timeout -- which is
// the working-config regression the ErrWaitDelay arm exists to prevent,
// arriving by the other door.
//
// The `runErr == nil` half is NOT the nanosecond read-ordering window a
// second version of this comment called it, and that claim was as wrong as
// the first. Once Process.Wait has reaped the child, watchCtx has handed
// off and nothing watches the context any more -- awaitGoroutines waits on
// its own WaitDelay timer, with no ctx involvement. So a drain that
// outlives the deadline but finishes INSIDE the grace returns literally
// nil with ctx.Err() already DeadlineExceeded, key in the buffer and no
// ErrWaitDelay anywhere. Measured: 10 raw runs out of 10. At the shipped
// 60s/2s that is the same two-second band, reached by the same helper.
//
// Both halves are pinned, and separately, which is what says the arm is
// covered rather than merely present.
// TestAnAPIKeyCmdThatAnsweredJustInsideItsBudgetKeepsItsAnswer takes the
// ErrWaitDelay half (drain longer than the grace) and
// TestAnAPIKeyCmdWhoseDrainOutlivesTheDeadlineKeepsItsAnswer the nil half
// (drain shorter than it). Demoting the whole arm fails both; demoting only
// `runErr == nil` and leaving ErrWaitDelay first fails the second alone --
// which is the check that they cover different things rather than the same
// thing twice. Nothing here survives now; two earlier versions of this
// paragraph claimed something did, and both were found wrong in review.
//
// Otherwise the DEADLINE is read before the exit code, which is
// load-bearing rather than tidy: exec.CommandContext kills the process and
// cmd.Run then reports an ordinary *exec.ExitError carrying -1, so
// exit-code-first would turn a deadlock into `run api_key_cmd: signal:
// killed` -- a deadlock reported as a decision, which is precisely what
// picker.classifyRun's own ordering comment exists to prevent.
//
// It is read as DeadlineExceeded specifically and never as `ctx.Err() != nil`.
// A caller that was CANCELLED reports context.Canceled, and calling a ⌃C a
// timeout would be false -- #272's hardest-won lesson, and not hypothetical:
// `create`'s pre-flight context is cancelled on a signal (main.watchSignals).
// The contract that goes with it, as #272's bounded states its own: a caller
// whose context carries a DEADLINE shorter than this one would have it
// reported as though it were ours. Neither caller does -- app.Bootstrap
// passes context.Background and create's passes context.WithCancel of it --
// and this package cannot be imported from outside the module.
//
// exec.ErrWaitDelay means the command SUCCEEDED. Its doc is explicit that it
// is returned only when "no Cancel call has occurred, and the command has
// otherwise exited with a successful status" -- i.e. api_key_cmd printed the
// key, exited 0, and a grandchild held the pipe past keyCmdWaitDelay. Read
// as an ordinary failure it would turn a working `pass show` into "your
// api_key_cmd is broken" after two seconds of dead time, so the buffered
// stdout is used and the run counts as the success it was.
//
// The obvious objection to those two budgets is that they are two timers on
// one deadline, which is what #272 was bitten by. They are not two timers on
// ONE deadline -- they measure different things, the deadline from the start
// of the command and the grace from the moment Wait observes it exited --
// but they are not mutually exclusive either, and the arm above is where
// that is measured and what it costs. Both can be true at once.
//
// What the two numbers do require is that they stay in their shipped
// RELATIONSHIP, sixty to two. Shrinking both to the same value makes a
// command that exits instantly expire the deadline first, which reaches the
// answer arm anyway now but would once have been reported as a timeout
// about nothing. That is stated because the test that pins this arm
// reproduced exactly that, 4 runs in 5, before the budgets were fixed
// rather than the code.
func runKeyCmd(ctx context.Context, apiKeyCmd []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, keyCmdTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, apiKeyCmd[0], apiKeyCmd[1:]...) //nolint:gosec // user-configured command, run intentionally
	cmd.WaitDelay = keyCmdWaitDelay
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	switch classifyRun(runErr, ctx.Err()) {
	case outcomeTimedOut:
		return "", timeout{prefix: keyCmdPrefix, what: "api_key_cmd gave no answer", deadline: keyCmdTimeout}
	case outcomeCancelled:
		return "", fmt.Errorf("%s: run api_key_cmd: %w", keyCmdPrefix, ctx.Err())
	case outcomeFailed:
		return "", fmt.Errorf("%s: run api_key_cmd: %w: %s", keyCmdPrefix, runErr, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// runOutcome is what one subprocess invocation amounted to.
type runOutcome int

const (
	outcomeAnswered runOutcome = iota
	outcomeTimedOut
	outcomeCancelled
	outcomeFailed
)

// classifyRun turns what cmd.Run returned, plus the context's own state,
// into one of four outcomes. It is a pure function of those two errors, and
// that is the point: the ORDER of these cases is the whole behaviour under
// discussion, and as a switch over live exec state it could only be tested
// by arranging real subprocesses to land in a window measured in hundreds
// of milliseconds.
//
// That was tried and it does not hold. Under CPU-bound processes inside the
// same cgroup quota -- a noisy neighbour in a container -- the shell can
// reach its final write and still not be scheduled to EXIT before the
// deadline, so the run is killed and classified as a timeout, correctly.
// Measured in review at a 500ms margin: clean at 8 same-cgroup burners,
// 5% failures at 32, 15% at 64, and 90% with a 50% CPU quota on top. No
// margin fixes that, because the margin can go negative; only removing the
// timing from the assertion does. This is #272's rule -- assert the cause,
// not the verdict -- applied to the sibling of the defect that taught it.
//
// THE ORDER, and why it is this one:
//
// An ANSWER IN HAND BEATS A CONTEXT THAT IS DONE, which is app.AwaitCheck's
// own rule. Both of the two ways cmd.Run says "the command answered" can
// arrive with the deadline already expired, and both are a real band rather
// than a race:
//
//   - runErr == nil. Once Process.Wait has reaped the child, watchCtx has
//     handed off and NOTHING watches the context any more -- awaitGoroutines
//     waits on its own WaitDelay timer (go1.26.4 src/os/exec/exec.go). A
//     drain that outlives the deadline but finishes inside the grace
//     therefore returns literally nil with ctx.Err() already
//     DeadlineExceeded. Measured raw: 10 runs out of 10.
//   - exec.ErrWaitDelay, which is returned only when "no Cancel call has
//     occurred, and the command has otherwise exited with a successful
//     status". A drain that outlives the grace as well gives that, with the
//     deadline expired alongside it. Measured raw: 20 runs out of 20.
//
// At the shipped 60s/2s both are the same two-second band: an api_key_cmd
// that answers between 58s and 60s and leaves a `gpg-agent`-shaped
// grandchild on the pipe. Reading the deadline first throws that working
// helper's key away -- the regression the ErrWaitDelay case exists to
// prevent, arriving by the other door. Three successive versions of this
// comment claimed one or other of these windows was impossible or
// nanoseconds wide; all three were wrong, and each was found so in review.
//
// A run the deadline AFFECTED does not reach the first case, and it takes
// one of two shapes rather than the one shape three earlier versions of
// this paragraph named:
//
//   - Killed while running. Process.Wait reports a non-zero state, so
//     cmd.Run returns an *exec.ExitError and the watcher's error is
//     preferred only `if err == nil`.
//   - Exited 0 before it could be reaped. Cancel's Kill succeeds on the
//     zombie, watchCtx injects ctx.Err(), Process.Wait then reports a ZERO
//     state -- so cmd.Run returns the CONTEXT error itself, not an
//     ExitError (go1.26.4 src/os/exec/exec.go). Measured by sweeping the
//     deadline across the exit instant: 20 runs in 400.
//
// Neither is nil and neither is ErrWaitDelay, so the conclusion holds
// either way: the first case cannot swallow a timeout. It is the mechanism
// that was stated too narrowly, which is the fourth time for this
// paragraph and the reason it now enumerates rather than generalises.
//
// The deadline is then read before the exit code, and as DeadlineExceeded
// SPECIFICALLY rather than `ctxErr != nil`: exec.CommandContext kills the
// process and cmd.Run reports an ordinary *exec.ExitError carrying -1, so
// exit-code-first turns a deadlock into `signal: killed` --
// picker.classifyRun's own documented hazard -- and a caller that was
// CANCELLED reports context.Canceled, which is not a timeout and must not
// say it was (#272).
func classifyRun(runErr, ctxErr error) runOutcome {
	switch {
	case runErr == nil || errors.Is(runErr, exec.ErrWaitDelay):
		return outcomeAnswered
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return outcomeTimedOut
	case ctxErr != nil:
		return outcomeCancelled
	default:
		return outcomeFailed
	}
}

// checkConfigPerm rejects an inline api_key literal when
// <configDir>/config.toml is readable or writable by anyone other than its
// owner. A missing config file has nothing to check and is allowed through
// (the literal must have come from somewhere other than a file this
// process can see, e.g. a test harness).
func checkConfigPerm(configDir string) error {
	path := filepath.Join(configDir, configFileName)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("resolve linear api key: stat %s: %w", path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("resolve linear api key: %s has permissions %04o, wider than 0600; refusing inline api_key", path, perm)
	}
	return nil
}
