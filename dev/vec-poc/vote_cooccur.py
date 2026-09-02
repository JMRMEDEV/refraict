#!/usr/bin/env python3
"""10-run VLM icon voting with tag co-occurrence phrase-context disambiguation
(technique 3).

For each VLM output phrase, resolve to the Lucide icon whose tag set has the
highest OVERLAP with the phrase's other words. This uses phrase context to
disambiguate an ambiguous alias: 'credit card' resolves to the credit-card icon
because both 'credit' and 'card' are in its tag set. A single word like 'card'
alone picks the icon whose full tag-set best matches the phrase context.

Falls back to icon-name priority (same as TF-IDF map) when context doesn't help
(single-word phrases with no discriminating context).

Compares against TF-IDF best (7.0/10, 4/5).
"""
import base64
import json
import re
import subprocess
from collections import Counter

OUT = "e2e-test/vec-poc"
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

# Load Lucide tags and build:
# - icon_tags: {normalized_icon_name: set of all tag tokens (including name words)}
# - alias_to_icons: {alias_token: [icon_names that have this token as tag/name word]}
TAGS_URL = "https://cdn.jsdelivr.net/npm/lucide-static/tags.json"
_raw = json.loads(subprocess.run(
    ["curl", "-s", "-m", "60", TAGS_URL], capture_output=True, text=True).stdout)

icon_tags = {}
alias_to_icons = {}
for name, taglist in _raw.items():
    c = " ".join(name.lower().replace("-", " ").split())
    toks = set(c.split())
    for t in taglist:
        for w in t.lower().split():
            toks.add(w)
    icon_tags[c] = toks
    for w in toks:
        alias_to_icons.setdefault(w, []).append(c)

# Also map each icon name to itself (priority for exact name match).
icon_name_set = set(icon_tags.keys())


def resolve(words):
    """Resolve a list of content words to the best-matching icon concept using
    phrase-context co-occurrence."""
    if not words:
        return ""

    # Gather candidate icons: any icon whose tag-set contains >= 1 phrase word.
    candidates = set()
    for w in words:
        for ic in alias_to_icons.get(w, []):
            candidates.add(ic)

    if not candidates:
        return ""

    # Score each candidate by overlap: how many of the phrase's words are in
    # this icon's tag set (the co-occurrence signal).
    phrase_set = set(words)
    best_ic, best_key = "", None
    for ic in candidates:
        score = len(phrase_set & icon_tags[ic])
        name_bonus = 1 if (ic in phrase_set or any(w in ic.split() for w in words)) else 0
        key = (score, name_bonus, -len(ic))
        if best_key is None or key > best_key:
            best_key = key
            best_ic = ic
    return best_ic


def canon(text):
    t = text.lower()
    if any(m in t for m in ("<svg", "```", "viewbox", "{", "http", "path fill")):
        return ""
    words = [w for w in re.findall(r"[a-z]+", t) if w not in STOP]
    if not words or len(words) > 4:
        return ""
    # Exact multi-word icon name match first.
    phrase = " ".join(words)
    if phrase in icon_name_set:
        return phrase
    # Co-occurrence resolution.
    return resolve(words)


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
    print(f"mean agreement (co-occurrence) = {sum(agrees)/len(agrees):.2f}/{RUNS}  per_icon={agrees}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
