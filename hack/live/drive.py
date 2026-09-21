#!/usr/bin/env python3
"""Drive the real herdr-draft form under a pty and print what a terminal
would show.

This is docs/manual-smoke.md's Route B, automated as far as it can be
automated: the same binary, launched the same way herdr launches it, with
every input it reads replaced by a stub under a scratch HOME. Nothing here
talks to a real herdr, spends an account, or creates anything.

Two things make it worth committing rather than rebuilding per pass.

The first is the stub `herdr`'s ENVELOPE. `internal/herdrc`'s runJSON
unmarshals `{"result": ...}` -- not `data`, not a bare value -- and
Bootstrap's first call is `workspace list`, which decodes the result into
`{"workspaces": [...]}`. A stub returning anything else fails the plugin
shut with `herdr-draft: herdr unreachable: herdr workspace list: parse
response: ...`, which reads like the binary was not found rather than like
the payload was the wrong shape. See stub_herdr below, and README.md.

The second is that a Bubble Tea program repaints PARTIALLY. Stripping ANSI
out of the raw stream and grepping it produces confident nonsense -- lines
that were overwritten half a frame ago, cursor jumps read as text order.
pyte is a terminal emulator, so what it holds is what a person would see.
"""

import argparse
import fcntl
import json
import os
import pty
import re
import select
import shutil
import signal
import struct
import subprocess
import sys
import termios
import time

MARKER = ".herdr-draft-live"

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))

# stub_herdr is the whole of #290's expensive knowledge. `workspace list`
# is Bootstrap's first call and the one that refuses; everything else the
# form asks for during an ordinary browse tolerates an empty result, and a
# subcommand that does not can be added here rather than rediscovered.
STUB_HERDR = """#!/bin/sh
case "$1 $2" in
  "workspace list") echo '{"result":{"workspaces":[]}}' ;;
  *)                echo '{"result":{}}' ;;
esac
"""

# stub_clauth answers `clauth status --json` (internal/clauth's Load).
# It is reached only because HOME is a scratch directory: Load prefers a
# FRESH ~/.clauth/status.json and never runs the CLI when one is there, so
# a driver that inherited a real HOME would read the real account state and
# this stub would never be called.
STUB_CLAUTH = """#!/bin/sh
cat <<'JSON'
{"schema": 1,
 "active_profile": "alpha",
 "generated_at": "2000-01-01T00:00:00Z",
 "refresh_interval_ms": 60000,
 "profiles": [
   {"name": "alpha", "active": true, "tier": "Max 20x", "auth_status": "ok",
    "windows": [{"label": "5h", "utilization_pct": 12.0, "resets_at": null},
                {"label": "week", "utilization_pct": 41.0, "resets_at": null}]},
   {"name": "beta", "active": false, "tier": "Pro", "auth_status": "ok",
    "windows": [{"label": "5h", "utilization_pct": 0.0, "resets_at": null},
                {"label": "week", "utilization_pct": 3.0, "resets_at": null}]}
 ]}
JSON
"""

sys.path.insert(0, HERE)
from stub_linear import STUB_LINEAR_KEY  # noqa: E402 -- the one fake key, defined once


def start_stub_linear(port, fixture_path):
    """Start stub_linear.py on 127.0.0.1:port and wait until it listens.

    A separate process, so this one stays single-threaded when it forks
    the pty child -- see stub_linear.py's own doc. Its stdin is a pipe
    held open by this process: closing it stops the stub, and so does this
    process dying, however it dies.
    """
    stub = subprocess.Popen(
        [sys.executable, os.path.join(HERE, "stub_linear.py"), str(port), fixture_path],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    line = stub.stdout.readline().strip()
    if line != "ready":
        stub.wait()
        raise SystemExit(
            "drive.py: the stub Linear did not start: %s\n"
            "           `just live` picks a free port and links the binary to it; if something took\n"
            "           the port in between, run it again" % (stub.stderr.read().strip() or line or "no reason given"))
    return stub


def stop_stub_linear(stub):
    stub.stdin.close()
    try:
        stub.wait(timeout=2)
    except subprocess.TimeoutExpired:
        stub.kill()
        stub.wait()


KEYS = {
    "tab": "\t",
    "shift-tab": "\x1b[Z",
    "enter": "\r",
    "esc": "\x1b",
    "space": " ",
    "backspace": "\x7f",
    "up": "\x1b[A",
    "down": "\x1b[B",
    "right": "\x1b[C",
    "left": "\x1b[D",
    "home": "\x1b[H",
    "end": "\x1b[F",
}


def key_bytes(token):
    """Translate one --keys token into what a terminal sends.

    `name*3` repeats. `ctrl-x` is the control code. Anything else is sent
    literally, one character at a time, which is how a single letter reaches
    a filter box.
    """
    token, _, count = token.partition("*")
    times = int(count) if count else 1
    if token in KEYS:
        out = KEYS[token]
    elif token.startswith("ctrl-") and len(token) == 6:
        out = chr(ord(token[5].lower()) - ord("a") + 1)
    elif len(token) == 1:
        out = token
    else:
        raise SystemExit("drive.py: unknown key %r (try tab, enter, down, ctrl-s, or a single character)" % token)
    return out * times


class Ordered(argparse.Action):
    """Collect --keys/--type/--click into ONE list, in the order given.

    argparse groups by flag, which would silently reorder `--keys tab
    --click 4,5 --keys down` into two keys then a click. A walk that is not
    the walk you asked for is the worst kind of wrong answer here, because
    it still prints a plausible screen.
    """

    def __call__(self, parser, ns, value, option_string=None):
        ns.actions = getattr(ns, "actions", None) or []
        ns.actions.append((self.dest, value))


def emulator(cols, rows):
    """A pyte screen and stream, with the two scrolls pyte 0.8.2 lacks.

    pyte has no SU (`CSI Ps S`) or SD (`CSI Ps T`) at all: its CSI table
    maps neither, so both fall to `debug`, a no-op. Bubble Tea's renderer
    uses SU to take lines out of the middle of the screen -- it scrolls the
    region, then repaints only what differs from the SCROLLED screen -- so
    in pyte the scroll never happened and the lines that should have left
    stayed. Found on #302's first list that shrinks mid-screen: filtering
    the issue picker from five issues to two left the last two rows of the
    five-issue frame on screen, printed by this driver as if the form had
    drawn them. A real terminal shows a clean screen; the form was right
    and this was not.

    They are written straight onto the buffer rather than through pyte's
    delete_lines / insert_lines, which have a bug of their own: they move a
    line only if its SOURCE is in pyte's sparse buffer, so a never-written
    blank line moving up leaves the line it should have replaced exactly
    as it was -- the same stale row by another route. Here the region is
    lifted out whole and put back shifted, and a row with nothing to put
    back is simply absent, which pyte reads as blank.
    """
    import pyte
    from pyte.screens import Margins

    class Screen(pyte.Screen):
        def scroll_up(self, count=None, **_):
            self._scroll(count or 1, 1)

        def scroll_down(self, count=None, **_):
            self._scroll(count or 1, -1)

        def _scroll(self, n, sign):
            top, bottom = self.margins or Margins(0, self.lines - 1)
            region = {y: self.buffer.pop(y) for y in list(self.buffer) if top <= y <= bottom}
            for y in range(top, bottom + 1):
                if y + sign * n in region:
                    self.buffer[y] = region[y + sign * n]
            self.dirty.update(range(top, bottom + 1))

    class Stream(pyte.ByteStream):
        csi = dict(pyte.ByteStream.csi, S="scroll_up", T="scroll_down")

    screen = Screen(cols, rows)
    return screen, Stream(screen)


class Term:
    def __init__(self, binary, env, cwd, cols, rows):
        self.cols, self.rows = cols, rows
        self.screen, self.stream = emulator(cols, rows)

        # openpty + fork rather than pty.fork(), so the window size is set
        # on the pty BEFORE the child exists. pty.fork() hands back a pty
        # of 0x0 and leaves the ioctl to the parent, which is a race the
        # child can win: it reads 0x0, renders, and only then takes the
        # SIGWINCH -- so what the screen holds is one layout laid over
        # another, at a size nobody asked for.
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        self.pid = os.fork()
        if self.pid == 0:
            os.close(master)
            os.setsid()
            fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
            for target in (0, 1, 2):
                os.dup2(slave, target)
            if slave > 2:
                os.close(slave)
            os.chdir(cwd)
            os.execve(binary, [binary], env)
        os.close(slave)
        self.fd = master

    def pump(self, seconds):
        end = time.time() + seconds
        while time.time() < end:
            r, _, _ = select.select([self.fd], [], [], max(0.0, end - time.time()))
            if not r:
                break
            try:
                data = os.read(self.fd, 65536)
            except OSError:  # the child is gone; the screen still holds its last paint
                break
            if not data:
                break
            self.stream.feed(data)

    def send(self, data, settle):
        os.write(self.fd, data.encode())
        self.pump(settle)

    def click(self, x, y, settle):
        # SGR mouse, 1-based. Press AND release: the form acts on the
        # release, and a press alone reads as a drag that never ends.
        self.send("\x1b[<0;%d;%dM" % (x + 1, y + 1), 0.15)
        self.send("\x1b[<0;%d;%dm" % (x + 1, y + 1), settle)

    def lines(self):
        return [l.rstrip() for l in self.screen.display]

    def full_repaint(self):
        """The screen after the program is made to redraw ALL of it.

        A resize away and back -- two SIGWINCHes -- makes Bubble Tea paint
        every line from scratch instead of the difference from its last
        frame. That is ground truth for the current state that owes nothing
        to how well this emulator applied the increments on the way there,
        which is what makes it the check for them: pyte dropping SU showed
        up as exactly such a disagreement, and nothing else did.
        """
        for rows in (self.rows + 1, self.rows):
            self.screen.resize(rows, self.cols)
            fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, self.cols, 0, 0))
            self.pump(0.8)
        return self.lines()

    def exited(self, grace=1.0):
        """The child's exit code, or None if it is still running.

        Polled to a deadline rather than asked once. pump() returns the
        instant the pty master reports EIO, which is BEFORE the child is
        reapable, so a single WNOHANG says "still running" for a binary
        that has already refused to start -- measured at roughly one run
        in ten, and the run then prints the refusal screen with row
        numbers and exits 0. That is the confident wrong reading this
        driver exists to avoid, in the one place it would be read as good
        news.
        """
        end = time.time() + grace
        while True:
            try:
                pid, status = os.waitpid(self.pid, os.WNOHANG)
            except ChildProcessError:
                return 0
            if pid:
                self.reaped = True
                return os.waitstatus_to_exitcode(status)
            if time.time() >= end:
                return None
            time.sleep(0.01)

    def close(self):
        """esc, then SIGTERM, and only then SIGKILL.

        SIGTERM rather than straight to KILL because the binary answers
        it: bubbletea turns SIGINT/SIGTERM into a QuitMsg, so the program
        reaches runProgram's teardown -- which cancels the Lifetime and
        waits for whatever it shelled out to (a configured picker) to
        die. Killing outright would orphan exactly the grandchild that
        convention exists to reap.
        """
        if not getattr(self, "reaped", False):
            try:
                os.write(self.fd, b"\x1b")
                time.sleep(0.2)
            except OSError:
                pass
            for sig, grace in ((signal.SIGTERM, 1.0), (signal.SIGKILL, 1.0)):
                try:
                    os.kill(self.pid, sig)
                except ProcessLookupError:
                    break
                if self.exited(grace) is not None:
                    break
        os.close(self.fd)


def write_exec(path, body):
    with open(path, "w") as f:
        f.write(body)
    os.chmod(path, 0o755)


def build_tree(root, fresh, keep_state, config, herdr_stub):
    """Make the scratch tree: a HOME with nothing in it, the two stubs, the
    plugin's config and state directories, and a throwaway git repo.

    The STATE directory is recreated on every run unless --keep-state. It
    holds recents.json / last-used.json / projects.json, so a second run
    would otherwise open on what the first run left behind -- a driver whose
    output depends on how often you have used it is not evidence.
    """
    # MARKER decides whether this directory is OURS, and the check is at
    # the top because --root is a path off the command line and two things
    # below it are rm -rf: --fresh on the whole tree, and the state wipe on
    # every ordinary run.
    #
    # The first version guarded only --fresh and planted the marker on
    # every run, which made a mistyped --root a two-step escalation: run
    # one silently deleted <path>/state AND adopted the directory, so run
    # two -- the same command one word longer -- passed the guard and took
    # the rest. An existing directory is therefore adopted only when it is
    # EMPTY, and the marker is written when the tree is made rather than
    # on every pass.
    marker = os.path.join(root, MARKER)
    ours = os.path.exists(marker)
    if os.path.exists(root) and not ours and os.listdir(root):
        raise SystemExit(
            "drive.py: %s exists, is not empty, and was not made by this driver -- refusing to use it\n"
            "           (it would delete %s/state, and --fresh would delete all of it)" % (root, root))
    if fresh and os.path.exists(root):
        shutil.rmtree(root, ignore_errors=True)
        ours = False
    home = os.path.join(root, "home")
    state = os.path.join(root, "state")
    if not keep_state:
        shutil.rmtree(state, ignore_errors=True)
    for d in (home, os.path.join(home, ".config"), os.path.join(home, ".local", "state"),
              os.path.join(root, "bin"), os.path.join(root, "cfg"), state):
        os.makedirs(d, exist_ok=True)
    stub = STUB_HERDR
    if herdr_stub:
        with open(herdr_stub) as f:
            stub = f.read()
    write_exec(os.path.join(root, "bin", "herdr"), stub)
    write_exec(os.path.join(root, "bin", "clauth"), STUB_CLAUTH)
    if not ours:
        with open(marker, "w") as f:
            f.write("made by hack/live/drive.py\n")
    # The config is decided afresh on EVERY run, exactly like the state
    # directory. It used to be written when --config was passed and left
    # alone otherwise, so one run's --config silently applied to every run
    # after it: a config handing the binary a wrong Linear key went on
    # putting the stub's refusal on screen for runs that never asked for
    # it. A driver whose output depends on what you ran last is the thing
    # the state wipe exists to prevent, and the config is the same hazard.
    dest = os.path.join(root, "cfg", "config.toml")
    if config:
        # Copied, then chmod 0600: shutil.copyfile carries content and not
        # mode, so the copy lands at the umask -- and internal/linear
        # refuses an inline api_key in a file readable by anyone else, so a
        # 0600 source silently became an unavailable issue row.
        shutil.copyfile(config, dest)
        os.chmod(dest, 0o600)
    elif os.path.exists(dest):
        os.remove(dest)
    repo = os.path.join(root, "repo")
    if not os.path.isdir(os.path.join(repo, ".git")):
        os.makedirs(repo, exist_ok=True)
        subprocess.run(["git", "init", "-q", "-b", "main", repo], check=True)
        subprocess.run(["git", "-C", repo, "-c", "user.name=live", "-c", "user.email=live@example.invalid",
                        "commit", "-q", "--allow-empty", "-m", "init"], check=True)
    return home, state, repo


# KEEP is the whole of what the child inherits, and it is an ALLOW-list
# because a deny-list of this was wrong in a way nothing on screen showed.
# The first version dropped every HERDR_* and both XDG_* and called that
# "nothing of yours"; it still handed over 130-odd variables, among them
# the Linear API key, which internal/linear reads straight from the
# environment (client.go: "2. The LINEAR_API_KEY environment variable").
# So the issue row was LIVE -- the form queried the real Linear API and
# wrote 163KB of a real workspace into the scratch state directory on
# every run. That is a screen that changes with whoever is at the
# keyboard, and a screen dump that carries real issue titles into a PR.
#
# Dropping GIT_* earns its place twice: `git rebase -x` exports GIT_DIR,
# and a child that inherited it would work on the wrong repository.
KEEP = ("LANG", "LANGUAGE", "TZ", "TMPDIR")


def child_env(root, home, state, repo, cols, rows, linear_key=None):
    """herdr's five variables, a locale, and nothing else of yours.

    Everything the form reads has to come from the scratch tree or it is
    not reproducible -- and worse, not safe to paste. An inherited
    HERDR_SOCKET_PATH would reach the real server behind the stub, an
    inherited Linear key reaches the real Linear, and an inherited
    SSH_AUTH_SOCK would let a `git fetch` authenticate.
    """
    env = {k: v for k, v in os.environ.items() if k in KEEP or k.startswith("LC_")}
    env.update(
        HOME=home,
        XDG_CONFIG_HOME=os.path.join(home, ".config"),
        XDG_STATE_HOME=os.path.join(home, ".local", "state"),
        # The stub bin first, then the ordinary places `git` lives --
        # /usr/local/bin and /opt/homebrew/bin included, or the child has
        # no git at all on a Mac.
        PATH=":".join([os.path.join(root, "bin"), "/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin"]),
        TERM="xterm-256color",
        COLUMNS=str(cols), LINES=str(rows),
        HERDR_BIN_PATH=os.path.join(root, "bin", "herdr"),
        HERDR_PLUGIN_ID="zvibaratz.draft",
        HERDR_PLUGIN_CONFIG_DIR=os.path.join(root, "cfg"),
        HERDR_PLUGIN_STATE_DIR=state,
        HERDR_PLUGIN_CONTEXT_JSON=json.dumps({
            "workspace_id": "ws-live", "workspace_cwd": repo,
            "tab_id": "tab-live", "focused_pane_id": "pane-live",
            "focused_pane_cwd": repo}),
    )
    if linear_key:
        # Set, not inherited: the one Linear key this child can hold.
        env["LINEAR_API_KEY"] = linear_key
    return env


def size(text):
    m = re.fullmatch(r"(\d+)x(\d+)", text)
    if not m:
        raise argparse.ArgumentTypeError("a size is WxH, e.g. 101x30")
    return int(m.group(1)), int(m.group(2))


def point(text):
    m = re.fullmatch(r"(\d+),(\d+)", text)
    if not m:
        raise argparse.ArgumentTypeError("a click is X,Y (0-based, from the top-left cell)")
    return int(m.group(1)), int(m.group(2))


def run_once(args, env_for, cols, rows):
    t = Term(args.binary, env_for(cols, rows), args.repo, cols, rows)
    try:
        t.pump(args.open_settle)
        code = t.exited()
        if code is not None:
            for line in t.lines():
                if line:
                    print(line)
            # Flushed before the diagnosis: stdout is block-buffered down a
            # pipe and stderr is not, so without this the explanation
            # arrives before the screen it is explaining.
            sys.stdout.flush()
            print("drive.py: the binary exited with status %d before anything was sent" % code, file=sys.stderr)
            print("drive.py: a parse-response failure here means the stub's envelope is wrong -- see hack/live/README.md",
                  file=sys.stderr)
            return 1
        for kind, value in args.actions or []:
            if kind == "keys":
                for token in re.split(r"[\s,]+", value.strip()):
                    t.send(key_bytes(token), args.settle)
            elif kind == "type":
                t.send(value, args.settle)
            elif kind == "click":
                t.click(value[0], value[1], args.settle)
        lines = t.lines()
        rc = 0
        if args.check_repaint:
            full = t.full_repaint()
            differ = [i for i, (a, b) in enumerate(zip(lines, full)) if a != b]
            if differ:
                # The screen printed below is still the INCREMENTAL one --
                # what a terminal fed these bytes shows -- because which of
                # the two is wrong is the question, not the answer. Either
                # this emulator mishandled a sequence (pyte's missing SU was
                # one) or the program's own incremental repaint is wrong,
                # and both are findings. A picker that re-scrolls differently
                # at the resize's intermediate height can also land here;
                # read the rows before concluding.
                sys.stdout.flush()
                print("drive.py: %dx%d: the screen disagrees with a full repaint of the same state at rows %s"
                      % (t.cols, t.rows, differ), file=sys.stderr)
                for i in differ[:5]:
                    print("drive.py:   %2d as painted  |%s|" % (i, lines[i]), file=sys.stderr)
                    print("drive.py:   %2d full repaint |%s|" % (i, full[i]), file=sys.stderr)
                rc = 1
        if args.row or args.line:
            for n in args.row:
                if not -len(lines) <= n < len(lines):
                    raise SystemExit("drive.py: --row %d is outside a %d-row screen" % (n, len(lines)))
                print(lines[n])
            for needle in args.line:
                for line in lines:
                    if needle in line:
                        print(line)
        else:
            for i, line in enumerate(lines):
                print("%2d %s" % (i, line))
        return rc
    finally:
        t.close()


def main(argv):
    p = argparse.ArgumentParser(
        prog="drive.py",
        description="run the real form under a pty and print the screen",
        epilog="actions apply in the order given: --keys tab,tab --click 4,5 --keys down")
    p.add_argument("--size", type=size, action="append", metavar="WxH",
                   help="terminal size, repeatable; the binary is relaunched per size (default 101x30, the popup's)")
    p.add_argument("--keys", action=Ordered, metavar="SEQ",
                   help="comma- or space-separated: tab, shift-tab, enter, esc, up/down/left/right, "
                        "ctrl-s, a single character, any of them with *N to repeat. Through `just live` "
                        "use commas -- a quoted value with spaces does not survive the recipe")
    p.add_argument("--type", action=Ordered, metavar="TEXT", help="type TEXT literally")
    p.add_argument("--click", type=point, action=Ordered, metavar="X,Y",
                   help="click cell X,Y -- the only way to reach a row Tab skips")
    p.add_argument("--row", type=int, action="append", default=[], metavar="N",
                   help="print row N instead of the screen; negative counts from the bottom, so -1 is the footer")
    p.add_argument("--line", action="append", default=[], metavar="SUBSTR",
                   help="print every row containing SUBSTR instead of the screen")
    p.add_argument("--binary", default=os.path.join(REPO, "bin", "herdr-draft"),
                   help="the binary to drive (default: this repo's bin/herdr-draft)")
    p.add_argument("--root", default=os.path.join(os.environ.get("TMPDIR", "/var/tmp"), "herdr-draft-live"),
                   help="scratch tree: HOME, the stubs, the plugin dirs and a throwaway repo")
    p.add_argument("--repo", help="drive the form at this repository instead of the throwaway one")
    p.add_argument("--config", metavar="FILE", help="install FILE as the plugin's config.toml")
    p.add_argument("--linear-port", type=int, metavar="PORT",
                   help="serve the stub Linear on this loopback port. Only a binary LINKED to that port "
                        "talks to it -- `just live` does both halves; without this the issue row is absent")
    p.add_argument("--linear", metavar="FILE", default=os.path.join(HERE, "linear-issues.json"),
                   help="the assignedIssues response the stub Linear serves (default: hack/live/linear-issues.json)")
    p.add_argument("--herdr", metavar="FILE",
                   help="install FILE as the stub herdr instead of the built-in one -- which is how you "
                        "answer more subcommands, and how you reproduce the envelope failure the README describes")
    p.add_argument("--fresh", action="store_true", help="delete the whole scratch tree first")
    p.add_argument("--keep-state", action="store_true",
                   help="keep the plugin state dir, so last-used and per-project memory carry over between runs")
    p.add_argument("--no-check-repaint", dest="check_repaint", action="store_false",
                   help="skip comparing the screen with a forced full repaint of the same state -- "
                        "faster, and the only guard against this emulator misreading an increment")
    p.add_argument("--settle", type=float, default=0.25, help="seconds to wait after each action (default 0.25)")
    p.add_argument("--open-settle", type=float, default=1.5,
                   help="seconds to wait after launch, before the first action (default 1.5)")
    p.set_defaults(actions=[])
    args = p.parse_args(argv)

    try:
        import pyte  # noqa: F401
    except ImportError:
        raise SystemExit(
            "drive.py needs pyte. Run it through `just live`, which builds a scratch venv\n"
            "with the pinned version -- the pin lives in the justfile and nowhere else.")

    if not os.path.isfile(args.binary) or not os.access(args.binary, os.X_OK):
        raise SystemExit("drive.py: no binary at %s -- run `just build`" % args.binary)

    home, state, repo = build_tree(args.root, args.fresh, args.keep_state, args.config, args.herdr)
    if args.repo:
        repo = os.path.abspath(args.repo)
    args.repo = repo

    stub = start_stub_linear(args.linear_port, args.linear) if args.linear_port else None
    key = STUB_LINEAR_KEY if stub else None
    try:
        sizes = args.size or [(101, 30)]
        rc = 0
        for cols, rows in sizes:
            if len(sizes) > 1:
                print("=== %dx%d" % (cols, rows))
            rc |= run_once(args, lambda c, r: child_env(args.root, home, state, repo, c, r, key), cols, rows)
        return rc
    finally:
        if stub:
            stop_stub_linear(stub)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
