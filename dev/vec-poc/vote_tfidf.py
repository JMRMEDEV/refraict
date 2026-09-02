#!/usr/bin/env python3
"""10-run VLM icon voting with the TF-IDF-weighted Lucide map (technique 1).

Canonicalization honors the 'weak' flag: prefer whole-phrase and multi-word
matches, then STRONG single-token aliases; only fall back to weak (common,
ambiguous) single-token aliases if nothing stronger resolved. This is the
TF-IDF payoff — common tokens like 'card'/'document' don't dominate the vote.

Compares against current best (Lucide+filter 6.2/10, 4/5).
"""
import base64
import json
import re
import subprocess
from collections import Counter

OUT = "e2e-test/vec-poc"
MAP = "dev/vec-poc/lucide/alias_map_tfidf.json"
MODEL = "llava-phi3"
RUNS = 10
PROMPT = ("You are shown a single cropped icon from a user interface. In two or "
          "three words, name what this icon is (e.g. 'search icon', 'settings "
          "gear', 'credit card', 'close button'). Reply with the name only.")

ICONS = [
    ("search", "magnifier / search"),
    ("usage_bar", "credit card (crop)"),
    ("billing", "billing / document"),
    ("close_x", "close X"),
    ("topup", "top up / card"),
]
STOP = {"icon", "button", "a", "an", "the", "of", "with", "gray", "grey",
        "image", "symbol", "glyph", "small", "dark", "on", "in", "it", "is",
        "this", "that", "appears", "to", "be", "represents", "and", "or", "for"}

with open(MAP) as f:
    ALIAS = json.load(f)


def lookup(key):
    return ALIAS.get(key)


def canon(text):
    t = text.lower()
    if any(m in t for m in ("<svg", "```", "viewbox", "{", "http", "path fill")):
        return ""
    words = [w for w in re.findall(r"[a-z]+", t) if w not in STOP]
    if not words or len(words) > 4:
        return ""
    # 1. whole phrase (multiword aliases like 'credit card', 'magnifying glass')
    d = lookup(" ".join(words))
    if d and not d["weak"]:
        return d["concept"]
    # 2. bigrams (strong first)
    for i in range(len(words) - 1):
        d = lookup(words[i] + " " + words[i + 1])
        if d and not d["weak"]:
            return d["concept"]
    # 3. strong single tokens
    for w in words:
        d = lookup(w)
        if d and not d["weak"]:
            return d["concept"]
    # 4. last resort: any resolution incl. weak
    d = lookup(" ".join(words))
    if d:
        return d["concept"]
    for w in words:
        d = lookup(w)
        if d:
            return d["concept"]
    return ""


def query(path):
    with open(path, "rb") as fp:
        b64 = base64.b64encode(fp.read()).decode()
    req = json.dumps({"model": MODEL, "prompt": PROMPT, "images": [b64],
                      "stream": False, "keep_alive": "60s"})
    p = subprocess.run(["curl", "-s", "-m", "120",
                        "http://localhost:11434/api/generate", "-d", req],
                       capture_output=True, text=True)
    try:
        return json.loads(p.stdout).get("response", "")
    except Exception:
        return ""


def main():
    agrees = []
    for stem, truth in ICONS:
        tally = Counter()
        keys = []
        for _ in range(RUNS):
            k = canon(query(f"{OUT}/{stem}_base.png"))
            keys.append(k or "∅")
            if k:
                tally[k] += 1
        best, k = (tally.most_common(1)[0] if tally else ("(none)", 0))
        agrees.append(k)
        print(f"=== {stem} (truth: {truth}) ===")
        print(f"   voted={best!r} agree={k}/{RUNS}")
        print(f"   keys={keys}\n")
    print(f"mean agreement (TF-IDF map) = {sum(agrees)/len(agrees):.2f}/{RUNS}  per_icon={agrees}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
