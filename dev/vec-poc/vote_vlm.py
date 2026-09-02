#!/usr/bin/env python3
"""Majority-vote VLM icon labeling: baseline vs vec-bg-removed vs vec-bg-kept.

For each icon and each variant, query the VLM RUNS times. Normalize each output
to a short key, then take the MODE (most-repeated answer) as the confident
label. The agreement count (how many of RUNS matched the mode) is the
confidence/self-consistency signal — the idea being that a repeated answer
("magnifier","magnifier","q","magnifier") is the reliable one.

Reports, per variant: the modal label per icon, agreement k/RUNS, and the mean
agreement across icons (higher = more self-consistent preprocessing).
"""
import base64
import json
import re
import subprocess
from collections import Counter

OUT = "e2e-test/vec-poc"
MODEL = "llava-phi3"
RUNS = 4
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
VARIANTS = [("baseline", "_base"), ("vec-bg-removed", "_vec"), ("vec-bg-kept", "_vecbg")]

STOP = {"icon", "button", "a", "an", "the", "of", "with", "gray", "grey",
        "image", "symbol", "glyph", "small", "dark", "on", "in", "it", "is",
        "this", "that"}


def normalize(text):
    """Reduce a VLM answer to a short comparable key: lowercase content words,
    drop stopwords/punctuation, keep up to the first 2 meaningful tokens.
    Garbage/long outputs collapse to '' (counts as a unique non-answer)."""
    t = text.lower()
    # reject obvious format-garbage
    if any(m in t for m in ("<svg", "```", "viewbox", "{", "http", "path fill")):
        return ""
    words = [w for w in re.findall(r"[a-z]+", t) if w not in STOP]
    if not words:
        return ""
    return " ".join(words[:2])


def query(path):
    with open(path, "rb") as f:
        b64 = base64.b64encode(f.read()).decode()
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
    variant_agreements = {v[0]: [] for v in VARIANTS}
    for stem, truth in ICONS:
        print(f"\n=== {stem}  (truth: {truth}) ===")
        for vname, suffix in VARIANTS:
            path = f"{OUT}/{stem}{suffix}.png"
            raws = [query(path) for _ in range(RUNS)]
            keys = [normalize(r) for r in raws]
            counts = Counter(k for k in keys if k)  # ignore empty/garbage
            if counts:
                label, k = counts.most_common(1)[0]
            else:
                label, k = "(no consistent answer)", 0
            variant_agreements[vname].append(k)
            raw_short = " | ".join(k or "∅" for k in keys)
            print(f"  {vname:15} mode={label!r} agree={k}/{RUNS}   runs=[{raw_short}]")

    print("\n=== AVERAGE SELF-CONSISTENCY (mean agreement k/RUNS across icons) ===")
    for vname, _ in VARIANTS:
        a = variant_agreements[vname]
        mean = sum(a) / len(a) if a else 0
        print(f"  {vname:15} mean_agreement={mean:.2f}/{RUNS}  per_icon={a}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
