#!/usr/bin/env python3
"""The one GraphQL query internal/linear sends, answered from a file.

    stub_linear.py PORT FIXTURE

Serves on 127.0.0.1:PORT, prints `ready` once it is listening, and exits
when its stdin closes -- which is how drive.py stops it, and why a driver
that dies, even by SIGKILL, cannot leave it running.

It is reached only by a binary linked with -X pointing internal/linear's
defaultEndpoint at this port (#302); `just live` does both halves. An
installed copy is built without that flag and always talks to Linear.

It is its own process rather than a thread in drive.py because drive.py
forks the pty child, and fork() in a multi-threaded process copies every
lock the other threads hold into a child that can never release them.
Python 3.12 warns about exactly that on every run; a separate process
makes the warning's premise false instead of silencing it.
"""

import http.server
import json
import sys
import threading

# STUB_LINEAR_KEY is the only Linear key the driven binary ever holds, and it
# is fake on purpose. drive.py's child environment is an allow-list, so no
# real key arrives by inheritance, and drive.py sets this one itself -- which
# beats an inline api_key in a config, but NOT an api_key_cmd, so drive.py
# refuses a config carrying one while the stub is on (refuse_real_linear).
# drive.py imports it from here so the two cannot disagree.
STUB_LINEAR_KEY = "lin_api_herdr_draft_live_stub_not_a_real_key"


class StubLinear(http.server.BaseHTTPRequestHandler):
    """Answer assignedIssues from the fixture; refuse everything else.

    A different query, or any key but STUB_LINEAR_KEY, gets a GraphQL error
    instead of the fixture. The form shows that as an unavailable issue row
    WITH the reason, so a changed query or a leaked credential is on screen
    rather than silently answered -- and the key itself is never printed.
    """

    fixture = b""

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        try:
            query = json.loads(body or b"{}").get("query", "")
        except ValueError:
            query = ""
        if self.headers.get("Authorization") != STUB_LINEAR_KEY:
            reply = {"errors": [{"message": "hack/live stub: refusing a key that is not the stub's own"}]}
        elif "assignedIssues" not in query:
            reply = {"errors": [{"message": "hack/live stub: not the assignedIssues query this stub answers"}]}
        else:
            reply = None
        out = self.fixture if reply is None else json.dumps(reply).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, *args):
        # Silent: the default writes each request line to stderr, which is
        # drive.py's stderr and would interleave with the screen it prints.
        pass


def main(argv):
    port, fixture = int(argv[0]), argv[1]
    with open(fixture, "rb") as f:
        StubLinear.fixture = f.read()
    try:
        server = http.server.ThreadingHTTPServer(("127.0.0.1", port), StubLinear)
    except OSError as e:
        print("cannot serve on 127.0.0.1:%d (%s)" % (port, e.strerror), file=sys.stderr, flush=True)
        return 1
    threading.Thread(target=server.serve_forever, daemon=True).start()
    print("ready", flush=True)
    sys.stdin.read()  # until drive.py closes the pipe, or dies
    server.shutdown()
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
