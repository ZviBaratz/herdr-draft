#!/usr/bin/env python3
"""svg.py's own tests: synthetic frames in, SVG out, and the real ones.

    just frames-selftest

Most cases hand render() a few bytes of SGR-coloured text and read the SVG
back through xml.etree, so every case is also a well-formedness check. The
expected colours and geometry come from the SGR definitions and svg.py's
grid constants, never from reading back a previous run.

The last class runs over the repository's own showcase frames and images:
every frame converts under --strict, and every committed image under
docs/images is byte-identical to drawing it again. That is a stronger check
than internal/app's TestShowcaseImagesAreCurrent, which compares digests
because it must not need Python; this one can afford to redraw.

Like everything under hack/, it stays out of `just check` and CI.
"""

import contextlib
import hashlib
import io
import os
import re
import sys
import tempfile
import unittest
import xml.etree.ElementTree as ET

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, HERE)
import svg  # noqa: E402

NS = "{http://www.w3.org/2000/svg}"
ESC = "\x1b["
BG = "48;2;10;10;10"  # the dominant background every synthetic frame paints
FG = "38;2;200;200;200"


def sgr(*codes):
    return ESC + ";".join(codes) + "m"


def line(*parts):
    """A frame line on the dominant background, as the form paints them."""
    return sgr(BG) + "".join(parts) + sgr()


def draw(text, **kw):
    """(root element, svg text, unknown counter) for frame text."""
    out, unknown = svg.render(text.encode("utf-8"), "frame.txt", **kw)
    return ET.fromstring(out), out, unknown


def texts(root):
    return [(t.text, dict(t.attrib)) for t in root.iter(NS + "text")]


def rects(root):
    """Every cell-layer rect, card excluded, as (x, y, w, h, fill)."""
    out = []
    for g in root.iter(NS + "g"):
        for r in g.iter(NS + "rect"):
            out.append((float(r.get("x")), float(r.get("y")), float(r.get("width")),
                        float(r.get("height")), r.get("fill")))
    return out


class Colours(unittest.TestCase):
    def test_truecolor_fg_and_bg(self):
        root, _, _ = draw(line(sgr(FG) + "xxxx", sgr("38;2;1;2;3", "48;2;4;5;6") + "A"))
        (text, attrs), = [t for t in texts(root) if t[0] == "A"]
        self.assertEqual(attrs["fill"], "#010203")
        self.assertIn((4 * svg.CELL_W, 0, svg.CELL_W, svg.CELL_H, "#040506"), rects(root))

    def test_fg_and_bg_in_separate_sequences_and_reset(self):
        frame = line(sgr("38;2;255;0;0") + "a" + sgr() + sgr(BG) + "b")
        root, _, _ = draw(frame, default_fg=(9, 9, 9))
        fills = {t: a["fill"] for t, a in texts(root)}
        self.assertEqual(fills, {"a": "#ff0000", "b": "#090909"})

    def test_39_and_49_restore_the_defaults(self):
        frame = line("zz", sgr("38;2;255;0;0", "48;2;0;255;0") + "a" + sgr("39", "49") + "b")
        root, _, _ = draw(frame, default_fg=(9, 9, 9), default_bg=(10, 10, 10))
        fills = {t: a["fill"] for t, a in texts(root)}
        self.assertEqual(fills["b"], "#090909")
        self.assertEqual([r for r in rects(root) if r[4] == "#00ff00"], [(18, 0, 9, 19, "#00ff00")])

    def test_the_card_is_the_dominant_background(self):
        root, _, _ = draw(line("abc") + "\n" + line(sgr("48;2;1;1;1") + "d"))
        card = root.find(NS + "rect")
        self.assertEqual(card.get("fill"), "#0a0a0a")
        # The one cell on another background is the only background rect.
        self.assertEqual(rects(root), [(0, svg.CELL_H, svg.CELL_W, svg.CELL_H, "#010101")])

    def test_no_foreground_takes_the_commonest_one_on_letters_not_rules(self):
        # Three rule cells in grey outnumber two letters in blue; the
        # unstyled `z` must still come out blue -- and so join their run.
        frame = line(sgr("38;2;128;128;128") + "\u2500" * 3 + sgr(BG, "38;2;0;0;255") + "ab" + sgr() + sgr(BG) + " z")
        root, _, _ = draw(frame)
        self.assertEqual([(t, a["fill"]) for t, a in texts(root)], [("ab z", "#0000ff")])

    def test_no_foreground_anywhere_falls_back_to_grey(self):
        root, _, _ = draw(line("abc"))
        self.assertEqual(texts(root)[0][1]["fill"], svg.hexc(svg.FALLBACK_FG))


class ReverseVideo(unittest.TestCase):
    def test_reverse_swaps_fg_and_bg(self):
        frame = line("aaaa", sgr("7", "38;2;255;0;0") + "X")
        root, _, _ = draw(frame)
        (_, attrs), = [t for t in texts(root) if t[0] == "X"]
        self.assertEqual(attrs["fill"], "#0a0a0a")  # the old background
        self.assertIn((4 * svg.CELL_W, 0, svg.CELL_W, svg.CELL_H, "#ff0000"), rects(root))

    def test_reverse_without_fg_paints_the_default_fg(self):
        root, _, _ = draw(line("aaaa", sgr("7") + "X"), default_fg=(1, 2, 3))
        self.assertIn((4 * svg.CELL_W, 0, svg.CELL_W, svg.CELL_H, "#010203"), rects(root))

    def test_27_ends_reverse(self):
        root, _, _ = draw(line("aaaa", sgr("7") + "X" + sgr("27") + "Y"), default_fg=(1, 2, 3))
        fills = {t: a["fill"] for t, a in texts(root)}
        self.assertEqual(fills["Y"], "#010203")


class Attributes(unittest.TestCase):
    def test_bold_italic_and_their_resets(self):
        frame = line(sgr(FG, "1") + "a" + sgr("3") + "b" + sgr("22") + "c" + sgr("23") + "d")
        root, _, _ = draw(frame)
        cls = {t: a.get("class", "") for t, a in texts(root)}
        self.assertEqual(cls, {"a": "b", "b": "b i", "c": "i", "d": ""})

    def test_dim_is_half_way_to_the_background(self):
        root, _, _ = draw(line(sgr("2", "38;2;210;210;210") + "a"))
        self.assertEqual(texts(root)[0][1]["fill"], svg.hexc(svg.mix((210,) * 3, (10,) * 3, 0.5)))


class Runs(unittest.TestCase):
    def test_same_style_segments_merge_into_one_run(self):
        # The form emits a reset and a fresh background between every styled
        # segment; two segments that resolve to one style are one run.
        frame = line(sgr(FG) + "ab" + sgr() + sgr(BG) + sgr(FG) + " cd")
        root, _, _ = draw(frame)
        (text, attrs), = texts(root)
        self.assertEqual(text, "ab cd")
        self.assertEqual(attrs["x"], "0")
        self.assertEqual(attrs["textLength"], str(5 * svg.CELL_W))
        self.assertEqual(attrs["lengthAdjust"], "spacingAndGlyphs")

    def test_a_style_change_starts_a_run(self):
        frame = line(sgr(FG) + "ab" + sgr("38;2;1;1;1") + "cd")
        root, _, _ = draw(frame)
        self.assertEqual([(t, a["x"], a["textLength"]) for t, a in texts(root)],
                         [("ab", "0", "18"), ("cd", "18", "18")])

    def test_two_spaces_split_a_run_and_trailing_spaces_are_dropped(self):
        root, _, _ = draw(line(sgr(FG) + " a b  c   "))
        self.assertEqual([(t, a["x"], a["textLength"]) for t, a in texts(root)],
                         [("a b", "9", "27"), ("c", "54", "9")])

    def test_a_symbol_is_drawn_alone_and_centred(self):
        root, _, _ = draw(line(sgr(FG) + "ab\u21b5cd"))
        got = [(t, a["x"], a.get("textLength"), a.get("text-anchor")) for t, a in texts(root)]
        self.assertEqual(got, [("ab", "0", "18", None), ("\u21b5", "22.5", None, "middle"), ("cd", "27", "18", None)])

    def test_latin1_stays_in_the_run(self):
        root, _, _ = draw(line(sgr(FG) + "a \u00b7 b"))
        self.assertEqual([t for t, _ in texts(root)], ["a \u00b7 b"])


class Widths(unittest.TestCase):
    def test_a_wide_glyph_takes_two_cells(self):
        root, out, _ = draw(line(sgr(FG) + "a\u65e5b"))
        frame = svg.parse(line(sgr(FG) + "a\u65e5b"))
        self.assertEqual(frame.cols, 4)
        got = [(t, a["x"]) for t, a in texts(root)]
        self.assertEqual(got, [("a", "0"), ("\u65e5", str(svg.CELL_W * 2)), ("b", str(svg.CELL_W * 3))])

    def test_a_wide_glyph_below_the_symbol_range_is_still_drawn_alone(self):
        # Hangul choseong are East Asian wide and sit below U+2000, so only
        # the width, not the code point, sends this one to its own element.
        root, _, _ = draw(line(sgr(FG) + "a\u1100b"))
        got = [(t, a["x"], a.get("text-anchor")) for t, a in texts(root)]
        self.assertEqual(got, [("a", "0", None), ("\u1100", str(svg.CELL_W * 2), "middle"), ("b", str(svg.CELL_W * 3), None)])

    def test_a_combining_mark_joins_its_base(self):
        frame = svg.parse(line(sgr(FG) + "e\u0301x"))
        self.assertEqual(frame.cols, 2)
        root, _, _ = draw(line(sgr(FG) + "e\u0301x"))
        (text, attrs), = texts(root)
        self.assertEqual((text, attrs["textLength"]), ("e\u0301x", str(2 * svg.CELL_W)))

    def test_grid_is_the_widest_line_and_short_lines_are_padded(self):
        root, _, _ = draw(line("abcdef") + "\n" + line("ab"))
        self.assertEqual(root.get("width"), str(6 * svg.CELL_W + 2 * svg.MARGIN))
        self.assertEqual(root.get("height"), str(2 * svg.CELL_H + 2 * svg.MARGIN))

    def test_a_trailing_newline_is_not_a_row(self):
        self.assertEqual(len(svg.parse(line("a") + "\n").rows), 1)


class Shapes(unittest.TestCase):
    def test_a_rule_is_one_rect_not_glyphs(self):
        root, _, _ = draw(line(" ", sgr("38;2;1;1;1") + "\u2500" * 5))
        self.assertEqual(texts(root), [])
        mid = svg.CELL_H // 2
        self.assertEqual(rects(root), [(svg.CELL_W, mid, 5 * svg.CELL_W, 1, "#010101")])

    def test_the_focus_bar_is_half_a_cell(self):
        root, _, _ = draw(line(" ", sgr("38;2;1;1;1") + "\u258c"))
        self.assertEqual(rects(root), [(svg.CELL_W, 0, svg.CELL_W / 2, svg.CELL_H, "#010101")])

    def test_blocks_stack_into_one_rect_across_rows(self):
        # A scrollbar: the same eighth-block on three rows is one rect, with
        # no seam for a scaled viewer to show.
        col = line(" ", sgr("38;2;1;1;1") + "\u2595")
        root, _, _ = draw("\n".join([col] * 3))
        (x, y, w, h, fill), = rects(root)
        self.assertEqual((x, y, h, fill), (svg.CELL_W * 1.875, 0, 3 * svg.CELL_H, "#010101"))

    def test_a_shade_is_a_blend_of_fg_into_bg(self):
        root, _, _ = draw(line(sgr("38;2;210;210;210") + "\u2591\u2591"))
        want = svg.hexc(svg.mix((210,) * 3, (10,) * 3, 0.25))
        self.assertEqual(rects(root), [(0, 0, 2 * svg.CELL_W, svg.CELL_H, want)])

    def test_a_gauge_is_fill_then_track(self):
        root, _, _ = draw(line(sgr("38;2;210;210;210") + "\u2588\u2588\u2591"))
        self.assertEqual([r[2] for r in rects(root)], [2 * svg.CELL_W, svg.CELL_W])


class Escaping(unittest.TestCase):
    def test_markup_in_text_and_title_is_escaped(self):
        root, out, _ = draw(line(sgr(FG) + 'a<b>&"c'), title='x < y & "z"')
        self.assertEqual(texts(root)[0][0], 'a<b>&"c')
        self.assertEqual(root.find(NS + "title").text, 'x < y & "z"')
        self.assertNotIn("<b>", out)

    def test_no_double_hyphen_inside_a_comment(self):
        # XML forbids `--` inside a comment, and a frame's name is the one
        # thing a comment carries from outside -- so a name that could end
        # it early, or smuggle markup into it, is refused rather than drawn.
        _, out, _ = draw(line("a"))
        for comment in re.findall(r"<!--(.*?)-->", out, re.S):
            self.assertNotIn("--", comment)
        for bad in ("a--b.txt", "a b.txt", "a&b.txt", "x-->.txt", "dir/a.txt"):
            with self.subTest(bad), self.assertRaises(ValueError):
                svg.render(line("a").encode(), bad)


class Unknown(unittest.TestCase):
    def test_an_unknown_sgr_is_skipped_and_reported(self):
        root, _, unknown = draw(line(sgr(FG, "5") + "a"))
        self.assertEqual(dict(unknown), {"SGR 5": 1})
        self.assertEqual(texts(root)[0][1]["fill"], "#c8c8c8")

    def test_256_colour_is_consumed_so_what_follows_still_applies(self):
        root, _, unknown = draw(line(sgr("38;5;123", "1") + "a"))
        self.assertEqual(dict(unknown), {"SGR 38;5;123": 1})
        self.assertEqual(texts(root)[0][1].get("class"), "b")

    def test_a_non_sgr_escape_is_reported_and_not_drawn(self):
        root, _, unknown = draw(line(sgr(FG) + "a" + ESC + "2K" + "\x1b]8;;http://x\x07b"))
        self.assertEqual(sorted(unknown), sorted(["escape " + repr(ESC + "2K"),
                                                  "escape " + repr("\x1b]8;;http://x\x07")]))
        self.assertEqual([t for t, _ in texts(root)], ["ab"])

    def test_strict_refuses_and_writes_nothing(self):
        with tempfile.TemporaryDirectory() as d:
            src, dst = os.path.join(d, "f.txt"), os.path.join(d, "f.svg")
            with open(src, "w", encoding="utf-8") as f:
                f.write(line(sgr("5") + "a"))
            with contextlib.redirect_stderr(io.StringIO()) as err:
                self.assertEqual(svg.main([src, "-o", dst, "--strict"]), 1)
            self.assertFalse(os.path.exists(dst))
            self.assertIn("SGR 5", err.getvalue())
            with contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(svg.main([src, "-o", dst]), 0)
            self.assertTrue(os.path.exists(dst))


class Provenance(unittest.TestCase):
    def test_deterministic(self):
        frame = line(sgr(FG) + "abc " + sgr("7") + "d") + "\n" + line(sgr("38;2;1;1;1") + "\u2500" * 4)
        self.assertEqual(draw(frame)[1], draw(frame)[1])

    def test_digests_name_the_frame_by_file_name_only(self):
        with tempfile.TemporaryDirectory() as d:
            src, dst = os.path.join(d, "showcase-x.txt"), os.path.join(d, "x.svg")
            data = line(sgr(FG) + "a").encode()
            with open(src, "wb") as f:
                f.write(data)
            svg.main([src, "-o", dst])
            with open(dst, encoding="utf-8") as f:
                out = f.read()
        self.assertIn(f"<!-- frame showcase-x.txt sha256={hashlib.sha256(data).hexdigest()} -->", out)
        with open(svg.__file__, "rb") as f:
            gen = hashlib.sha256(f.read()).hexdigest()
        self.assertIn(f"<!-- generator hack/frames/svg.py sha256={gen} -->", out)
        self.assertNotIn(d, out)


class TheRepositorysOwn(unittest.TestCase):
    """The showcase frames and the images drawn from them."""

    FRAMES = os.path.join(ROOT, "internal", "app", "testdata", "frames")
    IMAGES = os.path.join(ROOT, "docs", "images")

    def showcase(self):
        names = sorted(n for n in os.listdir(self.FRAMES) if n.startswith("showcase-"))
        self.assertTrue(names, "no showcase frames")
        return names

    def test_every_showcase_frame_converts_strictly(self):
        for name in self.showcase():
            with self.subTest(name), open(os.path.join(self.FRAMES, name), "rb") as f:
                out, unknown = svg.render(f.read(), name)
                self.assertEqual(dict(unknown), {})
                ET.fromstring(out)

    def test_every_committed_image_is_what_svg_py_draws_now(self):
        images = sorted(n for n in os.listdir(self.IMAGES) if n.endswith(".svg")) if os.path.isdir(self.IMAGES) else []
        drawn = 0
        for name in images:
            with open(os.path.join(self.IMAGES, name), encoding="utf-8") as f:
                committed = f.read()
            m = re.search(r"<!-- frame (\S+) sha256=", committed)
            if not m:
                continue
            drawn += 1
            title = ET.fromstring(committed).find(NS + "title")
            with self.subTest(name), open(os.path.join(self.FRAMES, m.group(1)), "rb") as f:
                again, _ = svg.render(f.read(), m.group(1), title.text if title is not None else None)
                self.assertEqual(again, committed, f"docs/images/{name} is stale: run `just screenshots`")
        self.assertEqual(drawn, len(self.showcase()), "an image per showcase frame")


if __name__ == "__main__":
    unittest.main()
