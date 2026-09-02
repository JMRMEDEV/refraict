#!/usr/bin/env python3
"""10-run VLM icon voting with Lucide-tags canonicalization.

Canonicalize each VLM label against the Lucide alias map (dev/vec-poc/lucide/
alias_map.json): try the whole normalized phrase, else map individual tokens to
their canonical concept and take the first resolved concept. Vote over RUNS and
report per-icon agreement + mean self-consistency, to compare against the
hand-map (6.0/10, 4/5) and WordNet-lexicon (4.2/10, 1/5) results.
"""
import base64
import json
import re
import subprocess

OUT = "e2e-test/vec-poc"
MAP = "dev/vec-poc/lucide/alias_map.json"
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


def canon(text):
    t = text.lower()
    if any(m in t for m in ("<svg", "```", "viewbox", "{", "http", "path fill")):
        return ""
    words = [w for w in re.findall(r"[a-z]+", t) if w not in STOP]
    # Over-long-run filter: a real icon name is a few words. Reject rambling
    # outputs before they inject spurious tokens into the vote (validated fix).
    if not words or len(words) > 4:
        return ""
    # (a) whole phrase (multiword aliases like "magnifying glass", "credit card")
    phrase = " ".join(words)
    if phrase in ALIAS:
        return ALIAS[phrase]
    # (b) try adjacent word pairs (bi-grams) then single tokens.
    for i in range(len(words) - 1):
        bg = words[i] + " " + words[i + 1]
        if bg in ALIAS:
            return ALIAS[bg]
    for w in words:
        if w in ALIAS:
            return ALIAS[w]
    return ""  # unresolved -> non-answer


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
    from collections import Counter
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
    print(f"mean agreement (Lucide map) = {sum(agrees)/len(agrees):.2f}/{RUNS}  per_icon={agrees}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
