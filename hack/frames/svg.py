#!/usr/bin/env python3
"""A golden frame, drawn as an SVG: the README's screenshots.

    python3 hack/frames/svg.py FRAME.txt -o OUT.svg [--title TEXT] [--strict]
    just screenshots        # every showcase frame -> docs/images/*.svg

A golden frame (internal/app/testdata/frames/*.txt) is what the form's
ViewAt returned: one line per terminal row, each a string of cells coloured
by SGR sequences in 24-bit colour. This reads those lines the way a
terminal would and draws the same cells: a rectangle per run of
background, a <text> per run of same-style glyphs, and rectangles for the
block and rule characters a font cannot be trusted to draw edge to edge.

Why frames and not a capture: a frame is asserted by `go test`, so an
image made from one cannot drift from the UI -- the frame fails first, and
the images are regenerated from the new one. It needs no terminal, no
font on the machine that makes it, and nothing of anyone's own: the
showcase fixtures in internal/app carry neutral names only.

What the viewer's font can and cannot change
--------------------------------------------
The SVG names a font stack, not a font, and cannot embed one: GitHub
serves an SVG as an <img>, which loads nothing external. So the one thing
that must not depend on the font is WHERE each glyph sits, and three rules
keep that true:

- Every run of plain text carries textLength = cells x CELL_W with
  lengthAdjust="spacingAndGlyphs", so it occupies exactly its cells
  whatever the font's advance is -- Consolas's 0.55em and Menlo's 0.6em
  both land on the same grid.
- A glyph at or above U+2000 (arrows, ⌃, ⇥, ✓, ▸, …) is its own element,
  centred in its cell with no textLength. Those are the glyphs a viewer's
  monospace font is most likely to lack, and a fallback font's advance
  inside a run would squeeze the whole run; alone, it cannot move a
  neighbour. A wide glyph (East Asian W/F) is centred on its two cells.
- Runs split at two or more spaces, so column alignment never rests on
  the renderer preserving whitespace (xml:space and white-space:pre are
  set as well).

Block elements (█ ▌ ▕ ░ …) and the light and heavy rules (─ ━ │) are
drawn as rectangles, not glyphs: a rule of glyphs shows seams wherever the
font's ─ stops short of its advance, and a focus bar or a gauge does the
same. The shades (░ ▒ ▓) become a solid blend of foreground into
background at 25/50/75%, the eye's average of the stipple a terminal draws.

Colours the frame does not set
------------------------------
A cell with no background gets the frame's most common background, which
is also the card's: the form paints its panel colour on every cell. A cell
with no foreground -- the footer's key hints, before #319 -- gets the most
common foreground among cells that carry a letter or digit. Counting every
cell would elect the rule colour, since the three rules are the longest
runs on screen. A frame with no foreground at all falls back to a neutral
light grey. --default-fg and --default-bg override either.

The card
--------
The grid sits on a rounded rectangle in its own background colour with a
MARGIN of it all round, and a hairline edge in a translucent neutral grey:
the dark card is obvious on GitHub's light page, and the edge is what keeps
it from sinking into the dark one.

Staleness
---------
The output carries two comments: the sha256 of the frame it was drawn
from (by file name, never by path, so no home directory is written into
the image) and the sha256 of this script. internal/app's
TestShowcaseImagesAreCurrent recomputes both, so `go test` -- with no
Python -- fails when a committed image is older than its frame or than
this converter, and says to run `just screenshots`.

Unknown sequences
-----------------
An SGR code or escape sequence this does not understand is skipped and
reported on stderr; with --strict it is an error. `just screenshots`
passes --strict, so a new attribute in the form's output stops the images
from being regenerated wrong without anyone noticing.

Standard library only, Python 3.11 or later, like everything under hack/.
Not a Go package and not on the install build path.
"""

import argparse
import collections
import hashlib
import os
import re
import sys
import unicodedata
from dataclasses import dataclass, field, replace

# The grid. CELL_W is FONT_SIZE x 0.6, the advance of Menlo, SF Mono and
# DejaVu Sans Mono; CELL_H is the ~1.27 line height a terminal at that size
# typically gives. BASELINE puts the text's x-height on the cell's middle.
FONT_SIZE = 15
CELL_W = 9
CELL_H = 19
BASELINE = 14
MARGIN = 12
RADIUS = 10
FONT_STACK = 'ui-monospace, SFMono-Regular, Menlo, Consolas, "DejaVu Sans Mono", monospace'

# The card's edge: a neutral grey at low opacity, so the same edge reads on
# a light page and a dark one without being a colour of the theme's.
EDGE = "#808080"
EDGE_OPACITY = "0.35"

# For a frame that sets no foreground anywhere, and for no background.
FALLBACK_FG = (208, 208, 208)
FALLBACK_BG = (30, 30, 30)

# Glyphs drawn as rectangles: (x0, y0, x1, y1) as fractions of the cell,
# and the shade blended in (1 is the foreground itself). A rule is instead
# an axis and a thickness in whole pixels, so it lands on the pixel grid.
BLOCKS = {
    chr(0x2588): (0, 0, 1, 1, 1),  # █ full block
    chr(0x2580): (0, 0, 1, 0.5, 1),  # ▀ upper half
    chr(0x2590): (0.5, 0, 1, 1, 1),  # ▐ right half
    chr(0x2594): (0, 0, 1, 0.125, 1),  # ▔ upper one eighth
    chr(0x2595): (0.875, 0, 1, 1, 1),  # ▕ right one eighth
    chr(0x2591): (0, 0, 1, 1, 0.25),  # ░ light shade
    chr(0x2592): (0, 0, 1, 1, 0.5),  # ▒ medium shade
    chr(0x2593): (0, 0, 1, 1, 0.75),  # ▓ dark shade
}
for _k in range(1, 8):
    BLOCKS[chr(0x2580 + _k)] = (0, 1 - _k / 8, 1, 1, 1)  # ▁ … ▇ lower k/8
    BLOCKS[chr(0x2590 - _k)] = (0, 0, _k / 8, 1, 1)  # ▏ … ▉ left k/8
RULES = {
    chr(0x2500): ("h", 1),  # ─ light horizontal
    chr(0x2501): ("h", 2),  # ━ heavy horizontal
    chr(0x2502): ("v", 1),  # │ light vertical
    chr(0x2503): ("v", 2),  # ┃ heavy vertical
}

# At or above this, a glyph is drawn alone and centred (see the docstring).
ISOLATE_FROM = 0x2000

GENERATOR = "hack/frames/svg.py"


@dataclass(frozen=True)
class Style:
    fg: tuple | None = None
    bg: tuple | None = None
    bold: bool = False
    dim: bool = False
    italic: bool = False
    underline: bool = False
    reverse: bool = False


@dataclass
class Cell:
    glyph: str | None  # None for the second half of a wide glyph
    style: Style
    wide: bool = False


@dataclass
class Frame:
    rows: list  # list[list[Cell]]
    unknown: collections.Counter = field(default_factory=collections.Counter)

    @property
    def cols(self):
        return max((len(r) for r in self.rows), default=0)


# A CSI sequence, an OSC string (BEL or ST terminated), or any other
# two-byte escape. Only a CSI ending in `m` is understood.
ESCAPE = re.compile(r"\x1b\[([0-9;:?<=>]*)([ -/]*)([@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b.?", re.S)


def cell_width(ch):
    """Terminal columns ch occupies: 0 for a combining or format character,
    2 for East Asian wide or fullwidth, else 1. Ambiguous is 1, which is
    what the form's own width function (charmbracelet/x/ansi) counts."""
    if unicodedata.combining(ch) or unicodedata.category(ch) in ("Mn", "Me", "Cf"):
        return 0
    if unicodedata.east_asian_width(ch) in ("W", "F"):
        return 2
    return 1


def apply_sgr(style, params, unknown):
    """style with one SGR sequence's parameters applied."""
    codes = params.replace(":", ";").split(";") if params else [""]
    i = 0
    while i < len(codes):
        c = codes[i]
        if c in ("", "0"):
            style = Style()
        elif c == "1":
            style = replace(style, bold=True)
        elif c == "2":
            style = replace(style, dim=True)
        elif c == "3":
            style = replace(style, italic=True)
        elif c == "4":
            style = replace(style, underline=True)
        elif c == "7":
            style = replace(style, reverse=True)
        elif c == "22":
            style = replace(style, bold=False, dim=False)
        elif c == "23":
            style = replace(style, italic=False)
        elif c == "24":
            style = replace(style, underline=False)
        elif c == "27":
            style = replace(style, reverse=False)
        elif c == "39":
            style = replace(style, fg=None)
        elif c == "49":
            style = replace(style, bg=None)
        elif c in ("38", "48", "58") and i + 1 < len(codes) and codes[i + 1] == "2":
            rgb = codes[i + 2 : i + 5]
            if c == "58" or len(rgb) != 3 or not all(v.isdigit() and int(v) < 256 for v in rgb):
                unknown["SGR " + ";".join(codes[i : i + 5])] += 1
            else:
                colour = tuple(int(v) for v in rgb)
                style = replace(style, **{"fg" if c == "38" else "bg": colour})
            i += 5
            continue
        elif c in ("38", "48", "58") and i + 1 < len(codes) and codes[i + 1] == "5":
            # 256-colour: consumed so the codes after it are read as codes,
            # and reported, since nothing here maps an index to a colour.
            unknown["SGR " + ";".join(codes[i : i + 3])] += 1
            i += 3
            continue
        else:
            unknown["SGR " + c] += 1
        i += 1
    return style


def parse(text):
    """The frame's rows of cells. SGR state carries across a line break,
    as it does in a terminal, though the form resets at every line end."""
    frame = Frame(rows=[])
    style = Style()
    for line in text.split("\n"):
        row = []
        pos = 0
        for m in ESCAPE.finditer(line):
            style = _cells(line[pos : m.start()], style, row, frame.unknown)
            pos = m.end()
            if m.group(3) == "m" and not m.group(2) and not re.search(r"[?<=>]", m.group(1) or ""):
                style = apply_sgr(style, m.group(1), frame.unknown)
            else:
                frame.unknown["escape " + repr(m.group(0))] += 1
        _cells(line[pos:], style, row, frame.unknown)
        frame.rows.append(row)
    # A trailing newline ends the last line rather than starting one more.
    if frame.rows and not frame.rows[-1] and text.endswith("\n"):
        frame.rows.pop()
    return frame


def _cells(chunk, style, row, unknown):
    for ch in chunk:
        if ch < " " or ch == "\x7f":
            unknown["control " + repr(ch)] += 1
            continue
        w = cell_width(ch)
        if w == 0:
            if row and row[-1].glyph is not None:
                row[-1].glyph += ch
            continue
        row.append(Cell(ch, style, wide=w == 2))
        if w == 2:
            row.append(Cell(None, style))
    return style


def most_common(counter, fallback):
    return counter.most_common(1)[0][0] if counter else fallback


def defaults(frame):
    """The default foreground and background, as the module doc says."""
    bgs = collections.Counter(c.style.bg for r in frame.rows for c in r if c.style.bg is not None)
    fgs = collections.Counter(
        c.style.fg
        for r in frame.rows
        for c in r
        if c.style.fg is not None and c.glyph and c.glyph[0].isalnum()
    )
    return most_common(fgs, FALLBACK_FG), most_common(bgs, FALLBACK_BG)


def mix(a, b, t):
    """t of the way from b to a: mix(fg, bg, 0.25) is a quarter-strength fg."""
    return tuple(round(bv + (av - bv) * t) for av, bv in zip(a, b))


def resolve(style, dfg, dbg):
    """The colours a terminal paints for style: defaults filled in,
    reverse applied, dim as a half-strength foreground."""
    fg = style.fg or dfg
    bg = style.bg or dbg
    if style.reverse:
        fg, bg = bg, fg
    if style.dim:
        fg = mix(fg, bg, 0.5)
    return fg, bg


def hexc(rgb):
    return "#%02x%02x%02x" % rgb


def num(v):
    """A coordinate, as short as it can be and the same on every run."""
    s = f"{v:.3f}".rstrip("0").rstrip(".")
    return "0" if s == "-0" else s


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;")


def merge_rects(rects):
    """rects as (x, y, w, h, fill), merged: horizontally where two share a
    row band and touch, then vertically where two share a column band and
    touch. Fewer elements, and no antialiased seam where a viewer scales
    the image to a fractional size."""
    out = []
    for r in sorted(rects, key=lambda r: (r[1], r[3], r[4], r[0])):
        if out:
            x, y, w, h, f = out[-1]
            if (y, h, f) == (r[1], r[3], r[4]) and abs(x + w - r[0]) < 1e-9:
                out[-1] = (x, y, w + r[2], h, f)
                continue
        out.append(r)
    merged = []
    for r in sorted(out, key=lambda r: (r[0], r[2], r[4], r[1])):
        if merged:
            x, y, w, h, f = merged[-1]
            if (x, w, f) == (r[0], r[2], r[4]) and abs(y + h - r[1]) < 1e-9:
                merged[-1] = (x, y, w, h + r[3], f)
                continue
        merged.append(r)
    return sorted(merged, key=lambda r: (r[1], r[0], r[3], r[2], r[4]))


def layers(frame, dfg, dbg):
    """The three layers, in paint order: background rectangles that differ
    from the card, block and rule rectangles, and text elements."""
    bg_rects, shapes, texts = [], [], []
    for y, row in enumerate(frame.rows):
        top = y * CELL_H
        for x, cell in enumerate(row):
            fg, bg = resolve(cell.style, dfg, dbg)
            if bg != dbg:
                bg_rects.append((x * CELL_W, top, CELL_W, CELL_H, hexc(bg)))
            g = cell.glyph
            if g in BLOCKS:
                x0, y0, x1, y1, t = BLOCKS[g]
                shapes.append((
                    x * CELL_W + x0 * CELL_W, top + y0 * CELL_H,
                    (x1 - x0) * CELL_W, (y1 - y0) * CELL_H, hexc(mix(fg, bg, t)),
                ))
            elif g in RULES:
                axis, thick = RULES[g]
                if axis == "h":
                    shapes.append((x * CELL_W, top + CELL_H // 2 - thick // 2, CELL_W, thick, hexc(fg)))
                else:
                    shapes.append((x * CELL_W + CELL_W // 2 - thick // 2, top, thick, CELL_H, hexc(fg)))
        texts.extend(text_runs(row, top + BASELINE, dfg, dbg))
    return merge_rects(bg_rects), merge_rects(shapes), texts


def text_key(style, dfg, dbg):
    fg, _ = resolve(style, dfg, dbg)
    return (hexc(fg), style.bold, style.italic, style.underline)


def text_runs(row, baseline, dfg, dbg):
    """The <text> elements for one row. A run is same-style glyphs below
    ISOLATE_FROM with at most one space between them; everything else is
    drawn alone, or (a block or rule) not as text at all."""
    out = []
    run = None  # [start col, text, cells, key]
    gap = 0

    def flush():
        nonlocal run
        if run:
            start, s, cells, key = run
            out.append(text_el(key, s, x=start * CELL_W, y=baseline, length=cells * CELL_W))
        run = None

    for col, cell in enumerate(row):
        g = cell.glyph
        if g is None:
            continue
        key = text_key(cell.style, dfg, dbg)
        if g == " " and not cell.style.underline:
            gap += 1  # two or more and the next glyph cannot join the run
            continue
        if g in BLOCKS or g in RULES:
            flush()
        elif cell.wide or ord(g[0]) >= ISOLATE_FROM:
            flush()
            span = 2 if cell.wide else 1
            out.append(text_el(key, g, x=col * CELL_W + span * CELL_W / 2, y=baseline, middle=True))
        elif run and run[3] == key and gap <= 1:
            run[1] += " " * gap + g
            run[2] += gap + 1
        else:
            flush()
            run = [col, g, 1, key]
        gap = 0
    flush()
    return out


def text_el(key, s, x, y, length=None, middle=False):
    fill, bold, italic, underline = key
    cls = " ".join(c for c, on in (("b", bold), ("i", italic), ("u", underline)) if on)
    attrs = [f'x="{num(x)}"', f'y="{num(y)}"', f'fill="{fill}"']
    if cls:
        attrs.append(f'class="{cls}"')
    if middle:
        attrs.append('text-anchor="middle"')
    else:
        attrs.append(f'textLength="{num(length)}" lengthAdjust="spacingAndGlyphs"')
    return f"<text {' '.join(attrs)}>{esc(s)}</text>"


def sha256_hex(b):
    return hashlib.sha256(b).hexdigest()


def generator_sha():
    with open(os.path.abspath(__file__), "rb") as f:
        return sha256_hex(f.read())


def render(data, name, title=None, default_fg=None, default_bg=None):
    """The SVG for frame bytes data, whose file is called name.

    Returns (svg text, the frame's unknown-sequence counter). name goes
    into a comment verbatim -- a comment has no escapes, and the Go test
    reads it back as a file name -- so it is refused unless it is one
    plain file name that cannot end the comment early."""
    if not re.fullmatch(r"[A-Za-z0-9._-]+", name) or "--" in name:
        raise ValueError(f"{name!r}: a frame's file name must be letters, digits, '.', '_' and single '-'")
    frame = parse(data.decode("utf-8"))
    dfg, dbg = defaults(frame)
    dfg = default_fg or dfg
    dbg = default_bg or dbg
    bgs, shapes, texts = layers(frame, dfg, dbg)

    gw, gh = frame.cols * CELL_W, len(frame.rows) * CELL_H
    w, h = gw + 2 * MARGIN, gh + 2 * MARGIN
    out = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" role="img">',
        f"<!-- generated by {GENERATOR}: do not edit, regenerate with `just screenshots` -->",
        f"<!-- frame {name} sha256={sha256_hex(data)} -->",
        f"<!-- generator {GENERATOR} sha256={generator_sha()} -->",
    ]
    if title:
        out.append(f"<title>{esc(title)}</title>")
    out += [
        "<style>",
        f"text{{font-family:{FONT_STACK};font-size:{FONT_SIZE}px;white-space:pre}}",
        ".b{font-weight:bold}.i{font-style:italic}.u{text-decoration:underline}",
        "</style>",
        f'<rect x="0.5" y="0.5" width="{w - 1}" height="{h - 1}" rx="{RADIUS}" fill="{hexc(dbg)}"'
        f' stroke="{EDGE}" stroke-opacity="{EDGE_OPACITY}"/>',
        f'<g transform="translate({MARGIN} {MARGIN})" shape-rendering="crispEdges">',
    ]
    for x, y, rw, rh, fill in bgs + shapes:
        out.append(f'<rect x="{num(x)}" y="{num(y)}" width="{num(rw)}" height="{num(rh)}" fill="{fill}"/>')
    out.append("</g>")
    out.append(f'<g transform="translate({MARGIN} {MARGIN})" xml:space="preserve">')
    out += texts
    out += ["</g>", "</svg>", ""]
    return "\n".join(out), frame.unknown


def colour_arg(s):
    m = re.fullmatch(r"#?([0-9a-fA-F]{6})", s)
    if not m:
        raise argparse.ArgumentTypeError(f"{s!r} is not a colour: want RRGGBB")
    v = m.group(1)
    return tuple(int(v[i : i + 2], 16) for i in (0, 2, 4))


def main(argv=None):
    p = argparse.ArgumentParser(description="Draw a golden frame as an SVG.")
    p.add_argument("frame", help="a golden frame, e.g. internal/app/testdata/frames/showcase-opening-101x30.txt")
    p.add_argument("-o", "--output", help="the SVG to write (default: stdout)")
    p.add_argument("--title", help="the image's <title>, which is its accessible name")
    p.add_argument("--strict", action="store_true", help="fail on an SGR code or escape this does not understand")
    p.add_argument("--default-fg", type=colour_arg, help="RRGGBB for cells with no foreground")
    p.add_argument("--default-bg", type=colour_arg, help="RRGGBB for cells with no background")
    a = p.parse_args(argv)

    with open(a.frame, "rb") as f:
        data = f.read()
    try:
        svg, unknown = render(data, os.path.basename(a.frame), a.title, a.default_fg, a.default_bg)
    except ValueError as e:
        print(f"{a.frame}: {e}", file=sys.stderr)
        return 2
    for what, n in sorted(unknown.items()):
        print(f"{a.frame}: skipped {what} ({n}x)", file=sys.stderr)
    if unknown and a.strict:
        print(f"{a.frame}: --strict: not writing an image that dropped something", file=sys.stderr)
        return 1
    if a.output:
        with open(a.output, "w", encoding="utf-8", newline="\n") as f:
            f.write(svg)
    else:
        sys.stdout.write(svg)
    return 0


if __name__ == "__main__":
    sys.exit(main())
