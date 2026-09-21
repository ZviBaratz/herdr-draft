#!/usr/bin/env python3
"""The emulator's own tests: drive.emulator() fed bytes, screen read back.

    just live-selftest

No binary, no pty, no stub -- every case is a byte string handed straight
to the stream `emulator(cols, rows)` returns, and the screen compared with
what a terminal shows. The expected screens come from the probes that found
#305's bugs and from the sequences' definitions, never from reading back
what the code under test produced.

It stays out of `just check` and CI, as everything under hack/ does (#290):
the gate must not start needing Python.
"""

import os
import random
import sys
import unittest

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


if __name__ == "__main__":
    unittest.main()
