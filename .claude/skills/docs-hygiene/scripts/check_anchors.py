#!/usr/bin/env python3
"""Validate relative Markdown links and #fragment anchors using GitHub's slug rules.

Independent of lychee — use both. Exit 0 = all links resolve, 1 = broken links.

Usage: check_anchors.py FILE_OR_DIR [FILE_OR_DIR ...]
"""
import os
import re
import sys
from collections import Counter

HEADING = re.compile(r"^(#{1,6})\s+(.*)")
LINK = re.compile(r"\]\(([^)\s]+)\)")
FENCE = re.compile(r"^(```|~~~)")


def github_slug(heading: str) -> str:
    """GitHub anchor slug: lowercase, spaces->hyphen, keep [a-z0-9-_], drop the rest."""
    text = heading.replace("`", "").strip().lower()
    out = []
    for ch in text:
        if ch.isalnum() or ch in "-_":
            out.append(ch)
        elif ch == " ":
            out.append("-")
        # any other character (punctuation, symbols, em dashes) is dropped
    return "".join(out)


def collect_md_files(paths):
    files = []
    for p in paths:
        if os.path.isdir(p):
            for root, _, names in os.walk(p):
                files.extend(os.path.join(root, n) for n in names if n.endswith(".md"))
        elif p.endswith(".md"):
            files.append(p)
    return sorted(set(os.path.normpath(f) for f in files))


def anchors_of(path):
    """All valid anchors in a file, including GitHub's -1/-2 suffixes for duplicates."""
    slugs = []
    in_fence = False
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            if FENCE.match(line):
                in_fence = not in_fence
                continue
            if in_fence:
                continue
            m = HEADING.match(line)
            if m:
                slugs.append(github_slug(m.group(2)))
    valid = set()
    seen = Counter()
    for s in slugs:
        valid.add(s if seen[s] == 0 else f"{s}-{seen[s]}")
        seen[s] += 1
    return valid


def linkable_text(path):
    """File content with fenced code blocks and inline code spans blanked out,
    so `](...)` inside code (e.g. Go generics) is not mistaken for a link."""
    out = []
    in_fence = False
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            if FENCE.match(line):
                in_fence = not in_fence
                continue
            if not in_fence:
                out.append(re.sub(r"`[^`]*`", "``", line))
    return "".join(out)


def main(argv):
    if not argv:
        print(__doc__)
        return 2
    files = collect_md_files(argv)
    anchors = {f: anchors_of(f) for f in files}
    known = set(anchors)
    bad = []
    for f in files:
        base = os.path.dirname(f)
        text = linkable_text(f)
        for m in LINK.finditer(text):
            target = m.group(1)
            if target.startswith(("http://", "https://", "mailto:", "#http")):
                continue
            path_part, _, frag = target.partition("#")
            resolved = f if not path_part else os.path.normpath(os.path.join(base, path_part))
            if path_part:
                if not os.path.exists(resolved):
                    bad.append(f"{f}: missing file: {target}")
                    continue
            if frag and resolved in known and frag not in anchors[resolved]:
                bad.append(f"{f}: bad anchor: {target}")
    total = sum(len(anchors[f]) for f in files)
    if bad:
        print(f"BROKEN ({len(bad)}):")
        print("\n".join(bad))
        return 1
    print(f"OK — {len(files)} files, {total} anchors, all relative links and fragments resolve")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
