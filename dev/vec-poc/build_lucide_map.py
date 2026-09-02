#!/usr/bin/env python3
"""Build a Lucide-tags alias->concept map for icon-label canonicalization.

Fetches lucide-static/tags.json ({icon-name: [tags...]}) and produces
alias_map.json: {alias_token_or_phrase -> canonical_concept}. The canonical
concept is the Lucide icon name (hyphen-split to words, joined by space). Both
the icon name's own words and each tag map to that concept.

When an alias is claimed by multiple icons, prefer the SHORTER icon name (more
generic/canonical, e.g. 'search' over 'search-check').

Output: dev/vec-poc/lucide/alias_map.json
"""
import json
import os
import subprocess

OUT = "dev/vec-poc/lucide"


def fetch_tags():
    url = "https://cdn.jsdelivr.net/npm/lucide-static/tags.json"
    p = subprocess.run(["curl", "-s", "-m", "60", url], capture_output=True, text=True)
    return json.loads(p.stdout)


def norm(s):
    return " ".join(s.lower().replace("-", " ").split())


def main():
    os.makedirs(OUT, exist_ok=True)
    tags = fetch_tags()
    # Two-pass so that an alias which is itself an icon NAME always wins over a
    # tag-derived mapping (e.g. "search" -> search, not "regex").

    # alias -> concept, with priority:
    #   1. alias == an icon name              -> map to itself (highest)
    #   2. alias is a WORD of an icon name     -> prefer these (name-derived)
    #   3. alias is only a tag                 -> lowest; shorter concept wins
    alias = {}
    locked = set()        # rule 1: alias is exactly an icon name
    name_derived = set()  # rule 2: alias came from an icon name's words

    def add(a, concept, kind="tag"):
        a = norm(a)
        if not a:
            return
        if kind == "name":
            alias[a] = concept
            locked.add(a)
            return
        if a in locked:
            return
        if kind == "nameword":
            # Prefer name-derived over tag-only; among name-derived, shorter wins.
            cur = alias.get(a)
            if a not in name_derived or cur is None or len(concept) < len(cur):
                alias[a] = concept
                name_derived.add(a)
            return
        # tag-only: never override a name-derived alias.
        if a in name_derived:
            return
        cur = alias.get(a)
        if cur is None or len(concept) < len(cur):
            alias[a] = concept

    # Pass 1: lock every icon name.
    for name in tags:
        c = norm(name)
        add(c, c, kind="name")
    # Pass 2: name-words (prefer over tags).
    for name in tags:
        concept = norm(name)
        for w in concept.split():
            add(w, concept, kind="nameword")
    # Pass 3: tags and tag-words (lowest priority).
    for name, taglist in tags.items():
        concept = norm(name)
        for t in taglist:
            tn = norm(t)
            add(tn, concept, kind="tag")
            for w in tn.split():
                add(w, concept, kind="tag")

    with open(f"{OUT}/alias_map.json", "w") as f:
        json.dump(alias, f)
    print(f"icons: {len(tags)}  aliases: {len(alias)}")
    # Spot-check the key UI metaphors.
    for probe in ["magnifier", "magnifying glass", "lens", "search",
                  "x", "cross", "close", "gear", "settings", "envelope",
                  "mail", "credit card", "card", "trash", "bell",
                  "bar chart", "chart", "document", "file"]:
        print(f"  {probe:18} -> {alias.get(norm(probe), '(none)')!r}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
