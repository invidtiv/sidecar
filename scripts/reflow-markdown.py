#!/usr/bin/env python3
"""Reflow Markdown files: join hard-wrapped paragraphs and list items into
single flowing lines, so text renders well at any width.

Leaves untouched: code fences, headings, tables, blockquotes, horizontal
rules, nested/sibling list structure (indentation preserved), and lines with
explicit hard breaks (trailing two spaces or tab).

Usage: reflow-markdown.py FILE [FILE...]
"""

import re, sys, pathlib

LIST_ITEM = re.compile(r"^(\s*)([-*+]|\d+[.)])\s+")
STRUCTURAL = re.compile(r"^(#{1,6}\s|\||>|-{3,}|\*{3,}|_{3,})")
UNDERLINE = re.compile(r"^\s*([-*=]|_{3,})\s*$")

def hard_break(s: str) -> bool:
    return s.endswith("  ") or s.endswith("\t")

def reflow(text: str) -> str:
    lines = text.split("\n")
    out: list[str] = []
    i = 0
    in_fence = False
    while i < len(lines):
        raw = lines[i]
        stripped = raw.strip()
        if stripped.startswith(("```", "~~~")):
            in_fence = not in_fence
            out.append(raw)
            i += 1
            continue
        if (
            in_fence
            or not stripped
            or STRUCTURAL.match(stripped)
            or UNDERLINE.match(stripped)
            or hard_break(raw)
        ):
            out.append(raw)
            i += 1
            continue
        # Start a run (list item or paragraph), preserving the original
        # indentation of its first line.
        indent = raw[: len(raw) - len(raw.lstrip())]
        parts: list[str] = [stripped]
        is_list = bool(LIST_ITEM.match(raw))
        broke_hard = hard_break(raw)
        i += 1
        while i < len(lines) and not broke_hard:
            nxt = lines[i]
            ns = nxt.strip()
            if (
                not ns
                or ns.startswith(("```", "~~~"))
                or STRUCTURAL.match(ns)
                or UNDERLINE.match(ns)
                or hard_break(nxt)
            ):
                break
            if LIST_ITEM.match(nxt):
                break  # nested/sibling item stands alone
            parts.append(ns)
            broke_hard = hard_break(nxt)
            i += 1
        out.append(indent + " ".join(parts))
    return "\n".join(out)

for p in sys.argv[1:]:
    path = pathlib.Path(p)
    path.write_text(reflow(path.read_text()))
    print(f"reflowed {path}")
