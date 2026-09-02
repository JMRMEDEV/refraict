#!/usr/bin/env python3
"""Pull react-icons names -> normalized icon-concept vocabulary.

Fetches the .d.ts of several icon sets from the jsDelivr CDN, extracts the
exported component names (e.g. FaShoppingCart), strips the 2-letter set prefix
and style words (Outline/Fill/Regular/Sharp/Two/Duotone), splits PascalCase into
words, and builds:
  - concepts.txt : one normalized multi-word concept per line (e.g. "shopping cart")
  - words.json   : {word: frequency} across all icon names (the icon lexicon)

Output dir: dev/vec-poc/reacticons/
"""
import json
import os
import re
import subprocess

SETS = ["fa", "fa6", "md", "ai", "bs", "fi", "io5", "ri", "tb", "hi2"]
STYLE = {"outline", "fill", "filled", "regular", "sharp", "two", "tone",
         "duotone", "round", "rounded", "line"}
OUT = "dev/vec-poc/reacticons"


def fetch(setid):
    url = f"https://cdn.jsdelivr.net/npm/react-icons/{setid}/index.d.ts"
    p = subprocess.run(["curl", "-s", "-m", "30", url], capture_output=True, text=True)
    return p.stdout


def split_pascal(name):
    # FaShoppingCart -> ["Shopping","Cart"]; drop 2-letter set prefix later.
    parts = re.findall(r"[A-Z][a-z0-9]*|[0-9]+", name)
    return parts


def main():
    os.makedirs(OUT, exist_ok=True)
    words = {}
    concepts = set()
    total_names = 0
    for setid in SETS:
        dts = fetch(setid)
        names = re.findall(r"export declare const (\w+):", dts)
        total_names += len(names)
        for n in names:
            parts = split_pascal(n)
            if not parts:
                continue
            # Drop the leading set-prefix token (Fa/Md/Ai/Bs/Fi/Io/Ri/Tb/Hi...).
            # The prefix is the first capitalized 1-3 letter chunk matching setid.
            if parts and parts[0].lower().startswith(setid[:2].lower()):
                parts = parts[1:]
            # Lowercase, drop style words and pure numbers.
            toks = []
            for p in parts:
                pl = p.lower()
                if pl in STYLE or pl.isdigit():
                    continue
                if len(pl) >= 2:
                    toks.append(pl)
            if not toks:
                continue
            concepts.add(" ".join(toks))
            for t in toks:
                words[t] = words.get(t, 0) + 1

    with open(f"{OUT}/concepts.txt", "w") as f:
        for c in sorted(concepts):
            f.write(c + "\n")
    with open(f"{OUT}/words.json", "w") as f:
        json.dump(words, f)

    print(f"sets pulled: {len(SETS)}  icon names: {total_names}")
    print(f"unique concepts: {len(concepts)}  unique words: {len(words)}")
    # Show the most common icon words (the core icon lexicon).
    top = sorted(words.items(), key=lambda kv: kv[1], reverse=True)[:40]
    print("top icon words:", ", ".join(f"{w}({n})" for w, n in top))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
