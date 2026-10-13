// Package machine is what herdr-draft knows about starting a session on
// another herdr machine (docs/specs/2026-10-13-machine-design.md, "machine
// spec"): which saved machine a selector names, where a project lives
// there, which base can be carried to it, and whether it can host the
// session at all. internal/app and internal/create both ask it, so the
// popup and the headless command cannot disagree about any of it.
//
// It decides; it does not configure. Which selector a project gets is
// defaults.Machine's job, from config.toml alone, and the runner and the
// ssh that reach the machine are internal/herdrc's and internal/gitx's.
package machine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// Choice is a resolved selector: this machine (Info is zero) or one saved
// herdr machine.
type Choice struct {
	// Selector is what config.toml or --machine said, as written.
	Selector string
	// Info is the saved profile it named, zero for this machine.
	Info herdrc.MachineInfo
}

// Remote reports whether c is another machine.
func (c Choice) Remote() bool { return c.Info.ID != "" }

// Label is how c is named to a person: the profile's label, or "local".
func (c Choice) Label() string {
	if !c.Remote() {
		return config.LocalMachine
	}
	return c.Info.Label
}

// Select resolves selector against the saved machines (machine spec §4.1):
// "local" (or nothing) is this machine; then a profile id; then an exact
// label; then an ssh target exactly one profile has. A label match and a
// target match that name different profiles are refused as ambiguous, as
// is a disabled profile. Whatever matched, the choice carries the whole
// profile, so herdr is always handed its id -- never a label it would have
// to look up again, and might find renamed or shared.
//
// The ssh-target step goes further than herdr's own selector, which takes
// an id or a label only (resolve_machine, src/cli/target.rs at v0.9.1). It
// is what lets config.toml say `dev` for a machine labelled `dev (EC2)`.
func Select(saved []herdrc.MachineInfo, selector string) (Choice, error) {
	sel := strings.TrimSpace(selector)
	if sel == "" || sel == config.LocalMachine {
		return Choice{Selector: config.LocalMachine}, nil
	}
	pick := func(m herdrc.MachineInfo) (Choice, error) {
		if !m.Enabled {
			return Choice{}, fmt.Errorf("herdr machine %q is disabled (herdr machine enable %s)", m.Label, m.ID)
		}
		return Choice{Selector: sel, Info: m}, nil
	}
	for _, m := range saved {
		if m.ID == sel {
			return pick(m)
		}
	}
	var byLabel, byTarget []herdrc.MachineInfo
	for _, m := range saved {
		if m.Label == sel {
			byLabel = append(byLabel, m)
		}
		if m.Target == sel {
			byTarget = append(byTarget, m)
		}
	}
	ambiguous := func(ms ...herdrc.MachineInfo) error {
		labels := make([]string, len(ms))
		for i, m := range ms {
			labels[i] = fmt.Sprintf("%s (%s)", m.Label, m.ID)
		}
		return fmt.Errorf("%q is ambiguous: it names %s; use a profile id", sel, strings.Join(labels, " and "))
	}
	switch {
	case len(byLabel) > 1:
		return Choice{}, ambiguous(byLabel...)
	case len(byLabel) == 1:
		for _, m := range byTarget {
			if m.ID != byLabel[0].ID {
				return Choice{}, ambiguous(byLabel[0], m)
			}
		}
		return pick(byLabel[0])
	case len(byTarget) > 1:
		return Choice{}, ambiguous(byTarget...)
	case len(byTarget) == 1:
		return pick(byTarget[0])
	}
	return Choice{}, fmt.Errorf("no saved herdr machine %q (see `herdr machine list`)", sel)
}

// Rel is the project key's path under home (machine spec §6.1): what a
// project carries to another machine, where it is looked for under that
// machine's own $HOME -- /home/zvi/quantivly/hub becomes
// /home/ubuntu/quantivly/hub. Both are canonical (pathx.CanonicalKey). A
// key outside home, or home itself, has no path to carry and is refused.
func Rel(home, key string) (string, error) {
	if home == "" {
		return "", errors.New("a session on another machine needs your home directory, and it could not be determined")
	}
	prefix := strings.TrimSuffix(home, string(filepath.Separator)) + string(filepath.Separator)
	if !strings.HasPrefix(key, prefix) {
		return "", fmt.Errorf("%s is not under your home directory, so there is no path to carry to another machine", key)
	}
	return filepath.ToSlash(strings.TrimPrefix(key, prefix)), nil
}

// RefReader is the four questions about the LAPTOP's clone a remote session
// asks: which ref a base names, origin's default branch, whether origin has
// a branch, and origin's URL. app's and create's git sources both embed it,
// so their fakes answer it.
type RefReader interface {
	FullRefName(ctx context.Context, dir, name string) (string, error)
	OriginHead(ctx context.Context, dir string) (string, error)
	OriginBranchExists(ctx context.Context, dir, name string) (bool, error)
	OriginURL(ctx context.Context, dir string) (string, error)
}

// RemoteBase is the base a remote session's worktree is cut from (machine
// spec §6.4). The worktree is cut on the remote after a fetch there, so the
// base must be a ref that fetch brings: origin's. Left at HEAD it becomes
// origin's default branch, since the remote checkout's HEAD is wherever
// someone last left it; origin/X stays; a local X becomes origin/X when
// origin has one. Anything else -- a branch origin lacks, a tag, another
// remote's branch, a commit -- is refused, naming the machine, because none
// of them is something the remote can be counted on to have.
//
// g reads the laptop's clone at dir, as of its last fetch.
func RemoteBase(ctx context.Context, g RefReader, dir, base, label string) (string, error) {
	switch strings.TrimSpace(base) {
	case "", "HEAD", "@":
		head, err := g.OriginHead(ctx, dir)
		if err != nil {
			return "", err
		}
		if head == "" {
			return "", fmt.Errorf("this clone does not know origin's default branch, so a base for %s has to be named (an origin/ branch)", label)
		}
		return head, nil
	}
	full, err := g.FullRefName(ctx, dir, base)
	if err != nil {
		return "", fmt.Errorf("%s is not a ref this clone knows, so %s cannot be sent it: %w", base, label, err)
	}
	if short, ok := strings.CutPrefix(full, "refs/remotes/origin/"); ok {
		return "origin/" + short, nil
	}
	if name, ok := strings.CutPrefix(full, "refs/heads/"); ok {
		exists, err := g.OriginBranchExists(ctx, dir, name)
		if err != nil {
			return "", err
		}
		if exists {
			return "origin/" + name, nil
		}
		return "", fmt.Errorf("%s has none of this laptop's local branches, and origin has no %s: push it, or use local", label, name)
	}
	return "", fmt.Errorf("%s can only be cut from an origin/ branch, which %s is not", label, base)
}

// NormalizeOrigin is an origin URL reduced to the repository it names,
// host and path, lowercase: dev rewrites ssh remotes to https (machine spec
// §7), so one repository arrives as git@github.com:o/r.git on one machine
// and https://github.com/o/r on another.
func NormalizeOrigin(url string) string {
	s := strings.TrimSpace(url)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	} else if i := strings.Index(s, ":"); i >= 0 && !strings.Contains(s[:i], "/") {
		s = s[:i] + "/" + s[i+1:]
	}
	if at := strings.Index(s, "@"); at >= 0 && (strings.Index(s, "/") < 0 || at < strings.Index(s, "/")) {
		s = s[at+1:]
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	return strings.ToLower(s)
}

// The three verdicts a probe can return, for create's exit codes (machine
// spec §7): unreachable is 3; no clone and the wrong repository are 2.
var (
	ErrUnreachable = errors.New("the machine is unreachable")
	ErrNoClone     = errors.New("the project is not on the machine")
	ErrWrongRepo   = errors.New("the machine has a different repository there")
)

type verdictError struct {
	kind error
	msg  string
}

func (e *verdictError) Error() string        { return e.msg }
func (e *verdictError) Is(target error) bool { return target == e.kind }

// Report is what the two probes found (machine spec §7).
type Report struct {
	// Workspaces is the remote server's workspace list: the forwarding
	// probe's answer, and the duplicate-title check's labels.
	Workspaces []herdrc.WorkspaceInfo
	// Clone is the ssh probe's answer.
	Clone gitx.CloneProbe
}

// HasBranch reports whether the remote clone already has a local branch
// name: herdr checks out an existing local branch and ignores the base, so
// this is the remote half of the duplicate-branch check.
func (r Report) HasBranch(name string) bool {
	for _, b := range r.Clone.Branches {
		if b == name {
			return true
		}
	}
	return false
}

// Judge is whether a probed machine can host the session, and where: the
// project's directory there, or the refusal. A worktree needs a repository;
// a plain session needs only the directory. When both clones name an
// origin, they must name the same repository.
func Judge(r Report, c Choice, rel string, worktree bool, laptopOrigin string) (string, error) {
	where := "~/" + rel
	switch {
	case !r.Clone.Exists:
		return "", &verdictError{ErrNoClone, fmt.Sprintf("%s is not on %s", where, c.Label())}
	case worktree && !r.Clone.IsRepo:
		return "", &verdictError{ErrNoClone, fmt.Sprintf("%s on %s is not a git repository", where, c.Label())}
	}
	if r.Clone.IsRepo && laptopOrigin != "" && r.Clone.Origin != "" &&
		NormalizeOrigin(laptopOrigin) != NormalizeOrigin(r.Clone.Origin) {
		return "", &verdictError{ErrWrongRepo, fmt.Sprintf("%s on %s is %s, not %s",
			where, c.Label(), r.Clone.Origin, laptopOrigin)}
	}
	return r.Clone.Dir, nil
}

// Prober runs machine spec §7's two probes of a remote machine.
type Prober interface {
	Probe(ctx context.Context, c Choice, rel string) (Report, error)
}

// CLIProber is the production Prober: the forwarding probe through Runner
// scoped to the profile id, and the clone probe over ssh to its target, in
// parallel. The deadline is the caller's ctx.
type CLIProber struct {
	Runner herdrc.Runner
	// Clone is nil for gitx.Over(target).ProbeClone; a test hands in its
	// own so no ssh runs.
	Clone func(ctx context.Context, target, rel string) (gitx.CloneProbe, error)
}

// Probe asks both and returns when both have answered or ctx is done. A
// failure of either is ErrUnreachable, in that probe's own words (herdr's
// unwrapped, or ssh's). A ctx that ended is returned as itself, never as
// unreachable: "we could not check" and "we checked, and no" are different
// sentences (CLAUDE.md, #202).
func (p CLIProber) Probe(ctx context.Context, c Choice, rel string) (Report, error) {
	clone := p.Clone
	if clone == nil {
		clone = func(ctx context.Context, target, rel string) (gitx.CloneProbe, error) {
			return gitx.Over(target).ProbeClone(ctx, rel)
		}
	}
	type wsAnswer struct {
		ws  []herdrc.WorkspaceInfo
		err error
	}
	type cloneAnswer struct {
		probe gitx.CloneProbe
		err   error
	}
	wsc := make(chan wsAnswer, 1)
	cc := make(chan cloneAnswer, 1)
	go func() {
		ws, err := p.Runner.On(c.Info.ID).WorkspaceList(ctx)
		wsc <- wsAnswer{ws, err}
	}()
	go func() {
		probe, err := clone(ctx, c.Info.Target, rel)
		cc <- cloneAnswer{probe, err}
	}()
	var ws wsAnswer
	var cl cloneAnswer
	for got := 0; got < 2; got++ {
		select {
		case ws = <-wsc:
		case cl = <-cc:
		case <-ctx.Done():
			return Report{}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	for _, err := range []error{ws.err, cl.err} {
		if err != nil {
			return Report{}, &verdictError{ErrUnreachable, c.Label() + " is unreachable: " + herdrc.Reason(err)}
		}
	}
	return Report{Workspaces: ws.ws, Clone: cl.probe}, nil
}
