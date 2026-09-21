#!/usr/bin/env python3
"""drive.py's own tests: the emulator fed bytes, and the Linear refusals.

    just live-selftest

No binary, no pty, no stub. Most cases are a byte string handed straight
to the stream `emulator(cols, rows)` returns, and the screen compared with
what a terminal shows. The expected screens come from the probes that found
#305's bugs and from the sequences' definitions, never from reading back
what the code under test produced. The rest hand refuse_real_linear configs
whose meaning was checked against internal/config's own loader (#314).

It stays out of `just check` and CI, as everything under hack/ does (#290):
the gate must not start needing Python.
"""

import argparse
import contextlib
import io
import os
import random
import shutil
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import drive  # noqa: E402

CSI = b"\x1b["


def at(row, text):
    """Cursor to 1-based `row`, column 1, then `text`."""
    return CSI + b"%d;1H" % row + text


def filled(rows, prefix=b"row"):
    """Every row written: `row0` on the first line, `row1` on the next..."""
    return b"".join(at(i + 1, prefix + b"%d" % i) for i in range(rows))


def screen(cols, rows, *chunks):
    """What emulator(cols, rows) shows after each chunk is fed in turn --
    each chunk being one pty read."""
    s, stream = drive.emulator(cols, rows)
    for chunk in chunks:
        stream.feed(chunk)
    return [line.rstrip() for line in s.display]


class ScrollUpDown(unittest.TestCase):
    """SU (`CSI Ps S`) and SD (`CSI Ps T`), which pyte 0.8.2 lacks."""

    def test_su_scrolls_the_whole_screen(self):
        self.assertEqual(
            screen(10, 6, filled(6), CSI + b"2S"),
            ["row2", "row3", "row4", "row5", "", ""],
        )

    def test_su_scrolls_only_inside_the_margins(self):
        self.assertEqual(
            screen(10, 6, filled(6), CSI + b"2;4r" + CSI + b"1S"),
            ["row0", "row2", "row3", "", "row4", "row5"],
        )

    def test_sd_scrolls_only_inside_the_margins(self):
        self.assertEqual(
            screen(10, 6, filled(6), CSI + b"2;4r" + CSI + b"1T"),
            ["row0", "", "row1", "row2", "row4", "row5"],
        )

    def test_su_over_a_sparse_buffer_leaves_no_stale_row(self):
        # Only rows 0 and 1 were ever written. Row 2 was never in pyte's
        # sparse buffer, so a shift that moves only rows it finds there
        # would leave `stale` where it was.
        self.assertEqual(
            screen(10, 4, at(1, b"keep") + at(2, b"stale"), CSI + b"1S"),
            ["stale", "", "", ""],
        )

    def test_an_omitted_or_zero_count_scrolls_one_line(self):
        for seq in (CSI + b"S", CSI + b"0S"):
            with self.subTest(seq=seq):
                self.assertEqual(
                    screen(10, 4, filled(4), seq),
                    ["row1", "row2", "row3", ""],
                )

    def test_a_count_past_the_region_clears_it(self):
        self.assertEqual(
            screen(10, 6, filled(6), CSI + b"2;4r" + CSI + b"9S"),
            ["row0", "", "", "", "row4", "row5"],
        )


class DeleteInsertLines(unittest.TestCase):
    """DL (`CSI Ps M`) and IL (`CSI Ps L`), both routed through `_shift`.
    No walk emits either yet, so this is the only thing that covers them.

    Only DL needs it on pyte 0.8.2: its delete_lines moves a row only when
    the source is in the sparse buffer, and leaves the destination as it
    was otherwise. Its insert_lines pops every row it passes, bottom up,
    so the IL cases pass with the override removed too. They pin what IL
    shows, whichever code produces it."""

    def test_dl_at_the_top_of_a_sparse_buffer_leaves_no_stale_row(self):
        # Rows 1 and 2 were never written. pyte's own delete_lines leaves
        # `A` in row 0.
        self.assertEqual(
            screen(6, 4, at(1, b"A") + at(4, b"D"), CSI + b"1;1H" + CSI + b"1M"),
            ["", "", "D", ""],
        )

    def test_il_inside_the_margins(self):
        self.assertEqual(
            screen(6, 5, filled(5, b"r"), CSI + b"2;4r" + CSI + b"3;1H" + CSI + b"1L"),
            ["r0", "r1", "", "r2", "r4"],
        )

    def test_dl_inside_the_margins(self):
        self.assertEqual(
            screen(6, 5, filled(5, b"r"), CSI + b"2;4r" + CSI + b"3;1H" + CSI + b"1M"),
            ["r0", "r1", "r3", "", "r4"],
        )

    def test_dl_and_il_outside_the_margins_change_nothing(self):
        for seq in (CSI + b"1M", CSI + b"1L"):
            with self.subTest(seq=seq):
                self.assertEqual(
                    screen(6, 5, filled(5, b"r"), CSI + b"2;4r" + CSI + b"1;1H" + seq),
                    ["r0", "r1", "r2", "r3", "r4"],
                )

    def test_dl_and_il_return_the_cursor_to_column_one(self):
        # The next character lands at the left edge, over the row that
        # moved in -- or into the blank one that was opened.
        for seq, row in ((CSI + b"1M", "X3"), (CSI + b"1L", "X")):
            with self.subTest(seq=seq):
                self.assertEqual(
                    screen(6, 5, filled(5, b"r"), CSI + b"2;4r" + CSI + b"3;4H" + seq + b"X")[2],
                    row,
                )


class AgainstAModel(unittest.TestCase):
    """SU, SD, DL and IL over random sparse screens, margins, cursor rows
    and counts, compared with the same four operations done on a plain
    list of lines -- which shares nothing with `_shift` but the spec. The
    measured cases above say what the screen looks like in the states
    someone probed; this says the states in between agree with them."""

    @staticmethod
    def model(lines, top, bottom, cursor, op, count):
        count = max(count, 1)
        if op in "LM":
            if not top <= cursor <= bottom:
                return lines
            top = cursor
        region = lines[top:bottom + 1]
        k = min(count, len(region))
        if op in "SM":
            region = region[k:] + [""] * k
        else:
            region = [""] * k + region[:len(region) - k]
        return lines[:top] + region + lines[bottom + 1:]

    def test_agrees_with_a_list_of_lines(self):
        rng = random.Random(305)
        for _ in range(2000):
            rows = rng.randint(2, 8)
            lines = [chr(65 + y) if rng.random() < 0.5 else "" for y in range(rows)]
            data = b"".join(at(y + 1, line.encode()) for y, line in enumerate(lines) if line)
            top, bottom = 0, rows - 1
            if rng.random() < 0.5:
                top = rng.randint(0, rows - 2)
                bottom = rng.randint(top + 1, rows - 1)
                data += CSI + b"%d;%dr" % (top + 1, bottom + 1)
            cursor = rng.randint(0, rows - 1)
            op = rng.choice("SMTL")
            param = rng.choice([b"", b"0", b"1", b"2", b"5"])
            count = int(param or 0)
            data += CSI + b"%d;1H" % (cursor + 1) + CSI + param + op.encode()
            with self.subTest(data=data):
                self.assertEqual(
                    screen(4, rows, data),
                    self.model(lines, top, bottom, cursor, op, count),
                )


class PytePremises(unittest.TestCase):
    """What the emulator's comments say about pyte 0.8.2 itself, checked
    on plain pyte, so a bump that changes one fails here, named, rather
    than leaving a comment that has stopped being true."""

    @staticmethod
    def plain(cols, rows, data):
        import pyte
        s = pyte.Screen(cols, rows)
        pyte.ByteStream(s).feed(data)
        return [line.rstrip() for line in s.display]

    def test_pyte_delete_lines_leaves_a_stale_row(self):
        # The case DeleteInsertLines pins, on the code it replaces.
        self.assertEqual(
            self.plain(6, 4, at(1, b"A") + at(4, b"D") + CSI + b"1;1H" + CSI + b"1M"),
            ["A", "", "D", ""],
        )

    def test_pyte_insert_lines_needs_no_override(self):
        # IL goes through _shift for symmetry, not repair: pyte's own
        # agrees with the model wherever the model test would take it.
        rng = random.Random(312)
        for _ in range(500):
            rows = rng.randint(2, 8)
            lines = [chr(65 + y) if rng.random() < 0.5 else "" for y in range(rows)]
            data = b"".join(at(y + 1, line.encode()) for y, line in enumerate(lines) if line)
            top, bottom = 0, rows - 1
            if rng.random() < 0.5:
                top = rng.randint(0, rows - 2)
                bottom = rng.randint(top + 1, rows - 1)
                data += CSI + b"%d;%dr" % (top + 1, bottom + 1)
            cursor = rng.randint(0, rows - 1)
            param = rng.choice([b"", b"0", b"1", b"2", b"5"])
            data += CSI + b"%d;1H" % (cursor + 1) + CSI + param + b"L"
            with self.subTest(data=data):
                self.assertEqual(
                    self.plain(4, rows, data),
                    AgainstAModel.model(lines, top, bottom, cursor, "L", int(param or 0)),
                )

    def test_pyte_draws_csi_equals_and_less_than_as_text(self):
        self.assertEqual(self.plain(10, 2, b"ab" + CSI + b"=0;1ucd")[0], "ab0;1ucd")
        self.assertEqual(self.plain(10, 2, b"ab" + CSI + b"<ucd")[0], "abucd")

    def test_pyte_already_swallows_csi_greater_than_u(self):
        # Why dropping `>` from the kitty removal survives every mutation.
        self.assertEqual(self.plain(10, 2, b"ab" + CSI + b">1ucd")[0], "abcd")


def every_split(before, seq, after):
    """The same bytes as one read and as two, cut at every point inside
    `seq` -- a pty read can end anywhere."""
    yield (before + seq + after,)
    for i in range(len(seq) + 1):
        yield (before + seq[:i], seq[i:] + after)


class KittyKeyboard(unittest.TestCase):
    """`CSI = … u`, `CSI > … u` and `CSI < … u` only set, push or pop
    keyboard flags. pyte abandons a CSI opening with `=` or `<` and draws
    the rest as text, so they are removed before it sees them -- whole, or
    cut in two by a read. pyte 0.8.2 already swallows `CSI > … u` as a
    private sequence, so that case would pass without the removal; it is
    here for the pyte that stops doing so."""

    def test_removed_at_every_split(self):
        for seq in (CSI + b"=0;1u", CSI + b">1u", CSI + b"<u"):
            for chunks in every_split(b"ab", seq, b"cd"):
                with self.subTest(chunks=chunks):
                    self.assertEqual(screen(10, 2, *chunks)[0], "abcd")


class IgnoredSequences(unittest.TestCase):
    """Sequences the form emits that pyte does not handle, measured to
    leave the screen unchanged. Cut at every point too, because the kitty
    removal holds back a trailing `ESC`, `ESC [` or unfinished
    `ESC [ =`/`<`/`>` in case it becomes one of its own, and what it holds
    back has to arrive with the next read."""

    def test_leave_the_screen_unchanged_at_every_split(self):
        for seq in (
            CSI + b">4m",       # modifyOtherKeys
            CSI + b"?u",        # kitty keyboard query
            CSI + b"?2026$p",   # mode query
            CSI + b"?5W",       # tab-stop reset
            CSI + b"?1003h",    # mouse, any motion
            CSI + b"?1006h",    # mouse, SGR reports
            CSI + b"?2004h",    # bracketed paste
        ):
            for chunks in every_split(b"before", seq, b"after"):
                with self.subTest(chunks=chunks):
                    self.assertEqual(screen(20, 2, *chunks)[0], "beforeafter")

    def test_the_alternate_screen_opens_blank_at_every_split(self):
        # A terminal clears the alternate screen on the way in, so text
        # drawn before `CSI ? 1049 h` is gone. pyte has no alternate screen
        # and would keep it, which is why nothing is drawn first here: this
        # pins what the emulator does with the sequence, on the only
        # screen where that agrees with a terminal -- an empty one.
        for chunks in every_split(b"", CSI + b"?1049h", b"after"):
            with self.subTest(chunks=chunks):
                self.assertEqual(screen(20, 2, *chunks)[0], "after")


class SplitReads(unittest.TestCase):
    """The same holding back, on a sequence that does move something."""

    def test_a_cursor_move_cut_in_two_still_lands(self):
        for chunks in every_split(b"", CSI + b"3;2H", b"X"):
            with self.subTest(chunks=chunks):
                self.assertEqual(screen(6, 4, *chunks), ["", "", " X", ""])


# The command every refused config below would run. Never executed here,
# and a FAKE key if it were: nothing in this file may name a real one.
FAKE_CMD = '["sh", "-c", "echo lin_api_FAKE_selftest"]'

# U+212A KELVIN SIGN. Go's strings.EqualFold folds it to `k`, so a quoted
# key spelled with it is api_key_cmd to internal/config. Built with chr()
# rather than written as an escape, so no tool on the way can turn it into
# a raw rune or a plain `K` without it showing.
KELVIN = chr(0x212A)


class RefuseRealLinear(unittest.TestCase):
    """drive.refuse_real_linear: with the stub on, the only Linear key a
    run holds is the stub's (#314).

    Every config in `refused` is one internal/config decodes into
    Linear.APIKeyCmd -- each was checked against config.Load, not assumed
    from the TOML spec. BurntSushi/toml matches a key to its field with
    strings.EqualFold when no exact match exists, which is why the case
    and Kelvin-sign spellings are here: a check on the exact key
    `linear.api_key_cmd` would pass them."""

    def refusal(self, body=None, binary_given=True, linear_port=9):
        """What refuse_real_linear says about a config holding `body` --
        str or bytes; None for no --config at all. Empty when it lets the
        run go ahead."""
        if isinstance(body, str):
            body = body.encode("utf-8")
        args = argparse.Namespace(binary_given=binary_given, linear_port=linear_port,
                                  config=None if body is None else "test.toml")
        try:
            drive.refuse_real_linear(args, body)
        except SystemExit as e:
            return str(e.code)
        return ""

    refused = {
        "a [linear] table": "[linear]\napi_key_cmd = %s\n",
        "indented": "[linear]\n  api_key_cmd = %s\n",
        "an inline table": "linear = { api_key_cmd = %s }\n",
        "a dotted key": "linear.api_key_cmd = %s\n",
        "a quoted key": '[linear]\n"api_key_cmd" = %s\n',
        "a literal-quoted key": "[linear]\n'api_key_cmd' = %s\n",
        "a quoted table name": '["linear"]\napi_key_cmd = %s\n',
        "a dotted key with a quoted part": "linear.'api_key_cmd' = %s\n",
        "an upper-case key": "[linear]\nAPI_KEY_CMD = %s\n",
        "an upper-case table": "[LINEAR]\napi_key_cmd = %s\n",
        "a mixed-case dotted key": "Linear.Api_Key_Cmd = %s\n",
        "a Kelvin-sign key": '[linear]\n"api_%sey_cmd" = %%s\n' % KELVIN,
        "beside an inline api_key": '[linear]\napi_key = "lin_api_FAKE_inline"\napi_key_cmd = %s\n',
        # Two tables that are one to internal/config. The command is in the
        # SECOND, which is what a check reading only the first match misses.
        "in the second of two linear tables": '[linear]\napi_key = "lin_api_FAKE_inline"\n[LINEAR]\napi_key_cmd = %s\n',
    }

    def test_every_spelling_internal_config_reads_is_refused(self):
        for name, body in self.refused.items():
            with self.subTest(name):
                self.assertRegex(self.refusal(body % FAKE_CMD), r"sets \[linear\] api_key_cmd")

    def test_a_config_tomllib_cannot_read_is_refused(self):
        # The first is valid TOML 1.1 -- a newline inside an inline table --
        # which BurntSushi/toml decodes into APIKeyCmd and tomllib rejects.
        # Refusing what this side cannot read is what keeps that disagreement
        # from being a way through.
        for name, body in {
            "a multi-line inline table": "linear = { api_key_cmd = %s,\n}\n" % FAKE_CMD,
            "not TOML at all": "[linear\n",
            "not UTF-8": b"[linear]\napi_key = \"\xff\"\n",
        }.items():
            with self.subTest(name):
                self.assertRegex(self.refusal(body), r"cannot read .* as TOML")

    def test_an_inline_api_key_alone_goes_ahead(self):
        # The driver's LINEAR_API_KEY beats an inline key, so the stub's
        # key is still the one sent.
        self.assertEqual(self.refusal('[linear]\napi_key = "lin_api_FAKE_inline"\n'), "")

    def test_a_mention_internal_config_does_not_read_goes_ahead(self):
        for name, body in {
            "a comment": "[linear]\n# api_key_cmd = %s\n",
            "a trailing comment": '[linear]\napi_key = "lin_api_FAKE_inline"  # not api_key_cmd = %s\n',
            "a line inside a multi-line string": '[linear]\nprompt_template = """\napi_key_cmd = %s\n"""\n',
            "another table": "[other]\napi_key_cmd = %s\n",
        }.items():
            with self.subTest(name):
                self.assertEqual(self.refusal(body % FAKE_CMD), "")

    def test_a_linear_that_is_not_a_table_goes_ahead(self):
        # config.Load fails each of these with "type mismatch for
        # config.LinearConfig", so the binary refuses to open and runs no
        # key command: there is nothing here to refuse, and nothing to
        # crash on either.
        for name, body in {
            "a number": "linear = 5\n",
            "a string": 'linear = "api_key_cmd"\n',
            "an array of tables": "[[linear]]\napi_key_cmd = %s\n" % FAKE_CMD,
        }.items():
            with self.subTest(name):
                self.assertEqual(self.refusal(body), "")

    def test_no_config_goes_ahead(self):
        self.assertEqual(self.refusal(None), "")

    def test_the_stub_without_a_linked_binary_is_refused(self):
        # Even with a config that would pass: the default bin/herdr-draft
        # sends the stub's key to the real Linear.
        for body in (None, '[linear]\napi_key = "lin_api_FAKE_inline"\n'):
            with self.subTest(body=body):
                self.assertRegex(self.refusal(body, binary_given=False), r"--linear-port needs --binary")

    def test_without_the_stub_no_linear_key_goes_through(self):
        # No stub means no key the default binary could misdeliver, so it
        # needs no --binary. But it also means no LINEAR_API_KEY, so an
        # inline api_key is no longer outranked: the binary would use it
        # against the real Linear (#318). api_key_cmd is a real key either
        # way.
        def stub_off(body):
            return self.refusal(body, binary_given=False, linear_port=None)

        self.assertRegex(stub_off(self.refused["an inline table"] % FAKE_CMD), r"sets \[linear\] api_key_cmd")
        for name, body in {
            "a [linear] table": '[linear]\napi_key = "lin_api_FAKE_inline"\n',
            "upper case": '[LINEAR]\nAPI_KEY = "lin_api_FAKE_inline"\n',
            "an inline table": 'linear = { api_key = "lin_api_FAKE_inline" }\n',
            "a dotted, quoted key": 'linear."api_key" = "lin_api_FAKE_inline"\n',
        }.items():
            with self.subTest(name):
                self.assertRegex(stub_off(body), r"sets \[linear\] api_key without the stub")
        for name, body in {
            "no key at all": '[linear]\nprompt_template = "lin_api_FAKE_not_a_key"\n',
            "api_key under another table": '[other]\napi_key = "lin_api_FAKE_inline"\n',
            "api_key in a comment": '[linear]\n# api_key = "lin_api_FAKE_inline"\n',
        }.items():
            with self.subTest(name):
                self.assertEqual(stub_off(body), "")


class MainRefusesFirst(unittest.TestCase):
    """main() refuses before writing anything, only with the stub on, and
    installs the config it checked -- not whatever the path holds by the
    time the tree is built."""

    # An executable that exists, so a run that is NOT refused gets past
    # the binary check and on to the tree: with a missing binary every
    # run stops before hold_root, and "nothing was written" holds whatever
    # the refusal does.
    BINARY = "/bin/true"

    def setUp(self):
        d = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, d)
        self.root = os.path.join(d, "root")
        self.config = os.path.join(d, "config.toml")
        with open(self.config, "w") as f:
            f.write("linear = { api_key_cmd = %s }\n" % FAKE_CMD)

    def main(self, *argv):
        try:
            with contextlib.redirect_stderr(io.StringIO()):
                drive.main(["--root", self.root] + list(argv))
        except SystemExit as e:
            return str(e.code)
        self.fail("drive.main ran to the end")

    def test_with_the_stub_on(self):
        for argv, refusal in (
            (["--linear-port", "9", "--binary", self.BINARY, "--config", self.config],
             r"sets \[linear\] api_key_cmd"),
            (["--linear-port", "9"], r"--linear-port needs --binary"),
            (["--linear-port", "9", "--binary", self.BINARY, "--config", self.config + ".missing"],
             r"cannot read .*\.missing"),
        ):
            with self.subTest(argv=argv):
                self.assertRegex(self.main(*argv), refusal)
                self.assertFalse(os.path.exists(self.root), "wrote the scratch tree before refusing")
                self.assertFalse(os.path.exists(self.root + ".lock"), "took the lock before refusing")

    def test_only_1_to_65535_is_a_port(self):
        # A falsy 0 once read as "no stub". The Arabic-Indic nine passes
        # str.isdigit() and int() alike.
        for p in ("0", "65536", "-1", "+9", "\u0669"):
            with self.subTest(port=p):
                self.assertEqual(self.main("--linear-port", p, "--binary", self.BINARY), "2")
                self.assertFalse(os.path.exists(self.root))

    def test_with_the_stub_off(self):
        # api_key_cmd is refused here too. Without the stub it gives the
        # default binary a real key for the real Linear, or gives a
        # stub-linked --binary run by hand a real key for a loopback port.
        # Neither is a run this driver exists for.
        self.assertRegex(self.main("--binary", self.BINARY, "--config", self.config),
                         r"sets \[linear\] api_key_cmd")
        self.assertFalse(os.path.exists(self.root))
        self.assertFalse(os.path.exists(self.root + ".lock"))
        # So is an inline api_key, which nothing outranks without the stub.
        with open(self.config, "w") as f:
            f.write('[linear]\napi_key = "lin_api_FAKE_inline"\n')
        self.assertRegex(self.main("--binary", self.BINARY, "--config", self.config),
                         r"sets \[linear\] api_key without the stub")
        self.assertFalse(os.path.exists(self.root))
        self.assertFalse(os.path.exists(self.root + ".lock"))
        # The binary requirement is the stub's alone: with no stub and a
        # config naming no key, it gets as far as looking for the binary.
        with open(self.config, "w") as f:
            f.write('[linear]\nprompt_template = "x"\n')
        self.assertRegex(self.main("--config", self.config, "--binary", "/nonexistent/herdr-draft"),
                         r"no binary at")
        self.assertFalse(os.path.exists(self.root))

    def test_installs_the_config_it_checked(self):
        # hold_root can wait on another run for as long as that run lasts.
        # Anything that rereads the path after it installs whatever the
        # file says THEN -- here, an api_key_cmd written during the wait.
        checked = b'[linear]\napi_key = "lin_api_FAKE_inline"\n'
        with open(self.config, "wb") as f:
            f.write(checked)

        def hold_root(root):
            with open(self.config, "w") as f:
                f.write("[linear]\napi_key_cmd = %s\n" % FAKE_CMD)

        class Stop(Exception):
            pass

        def start_stub_linear(port, fixture):
            raise Stop()

        with mock.patch.object(drive, "hold_root", hold_root), \
                mock.patch.object(drive, "start_stub_linear", start_stub_linear):
            with self.assertRaises(Stop):
                drive.main(["--root", self.root, "--linear-port", "9", "--binary", self.BINARY,
                            "--config", self.config])
        dest = os.path.join(self.root, "cfg", "config.toml")
        with open(dest, "rb") as f:
            self.assertEqual(f.read(), checked)
        self.assertEqual(os.stat(dest).st_mode & 0o777, 0o600)


if __name__ == "__main__":
    unittest.main()
