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


class Term:
    def __init__(self, binary, env, cwd, cols, rows):
        import pyte

        self.cols, self.rows = cols, rows
        self.screen = pyte.Screen(cols, rows)
        self.stream = pyte.ByteStream(self.screen)

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

    def exited(self):
        try:
            pid, status = os.waitpid(self.pid, os.WNOHANG)
        except ChildProcessError:
            return 0
        return os.waitstatus_to_exitcode(status) if pid else None

    def close(self):
        try:
            os.write(self.fd, b"\x1b")
            time.sleep(0.2)
        except OSError:
            pass
        for sig in (signal.SIGKILL,):
            try:
                os.kill(self.pid, sig)
            except ProcessLookupError:
                break
        try:
            os.waitpid(self.pid, 0)
        except ChildProcessError:
            pass
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
    # MARKER guards --fresh, which is an rm -rf of a path that came off the
    # command line. A tree this driver did not make is never deleted, so a
    # slip of --root costs an error message rather than someone's work.
    marker = os.path.join(root, MARKER)
    if fresh and os.path.exists(root):
        if not os.path.exists(marker):
            raise SystemExit("drive.py: %s exists and was not made by this driver -- refusing --fresh" % root)
        shutil.rmtree(root, ignore_errors=True)
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
    with open(marker, "w") as f:
        f.write("made by hack/live/drive.py\n")
    write_exec(os.path.join(root, "bin", "clauth"), STUB_CLAUTH)
    if config:
        shutil.copyfile(config, os.path.join(root, "cfg", "config.toml"))
    repo = os.path.join(root, "repo")
    if not os.path.isdir(os.path.join(repo, ".git")):
        os.makedirs(repo, exist_ok=True)
        subprocess.run(["git", "init", "-q", "-b", "main", repo], check=True)
        subprocess.run(["git", "-C", repo, "-c", "user.name=live", "-c", "user.email=live@example.invalid",
                        "commit", "-q", "--allow-empty", "-m", "init"], check=True)
    return home, state, repo


def child_env(root, home, state, repo, cols, rows):
    """herdr's five variables, and nothing of yours.

    Every HERDR_* and both XDG_* are dropped rather than overridden: this
    driver is usually run from inside a herdr pane, and an inherited
    HERDR_SOCKET_PATH or HERDR_SESSION reaches the stub and then the real
    server behind it.
    """
    env = {k: v for k, v in os.environ.items()
           if not k.startswith("HERDR_") and k not in ("XDG_CONFIG_HOME", "XDG_STATE_HOME")}
    env.update(
        HOME=home,
        XDG_CONFIG_HOME=os.path.join(home, ".config"),
        XDG_STATE_HOME=os.path.join(home, ".local", "state"),
        PATH=os.path.join(root, "bin") + ":/usr/bin:/bin",
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
                for token in value.split():
                    t.send(key_bytes(token), args.settle)
            elif kind == "type":
                t.send(value, args.settle)
            elif kind == "click":
                t.click(value[0], value[1], args.settle)
        lines = t.lines()
        if args.row or args.line:
            for n in args.row:
                print(lines[n])
            for needle in args.line:
                for line in lines:
                    if needle in line:
                        print(line)
        else:
            for i, line in enumerate(lines):
                print("%2d %s" % (i, line))
        return 0
    finally:
        t.close()


def main(argv):
    p = argparse.ArgumentParser(
        prog="drive.py",
        description="run the real form under a pty and print the screen",
        epilog="actions apply in the order given: --keys 'tab tab' --click 4,5 --keys down")
    p.add_argument("--size", type=size, action="append", metavar="WxH",
                   help="terminal size, repeatable; the binary is relaunched per size (default 101x30, the popup's)")
    p.add_argument("--keys", action=Ordered, metavar="SEQ",
                   help="space-separated: tab, shift-tab, enter, esc, up/down/left/right, ctrl-s, "
                        "a single character, any of them with *N to repeat")
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
    p.add_argument("--herdr", metavar="FILE",
                   help="install FILE as the stub herdr instead of the built-in one -- which is how you "
                        "answer more subcommands, and how you reproduce the envelope failure the README describes")
    p.add_argument("--fresh", action="store_true", help="delete the whole scratch tree first")
    p.add_argument("--keep-state", action="store_true",
                   help="keep the plugin state dir, so last-used and per-project memory carry over between runs")
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

    sizes = args.size or [(101, 30)]
    rc = 0
    for cols, rows in sizes:
        if len(sizes) > 1:
            print("=== %dx%d" % (cols, rows))
        rc |= run_once(args, lambda c, r: child_env(args.root, home, state, repo, c, r), cols, rows)
    return rc


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
