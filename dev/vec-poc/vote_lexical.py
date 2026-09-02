#!/usr/bin/env python3
"""Baseline + repetition voting with lightweight lexical grouping (Option C).

Baseline images only (vectorization was rejected by earlier averaged tests).
Run the VLM RUNS times per icon, then GROUP semantically-equal answers so
repetitions combine, and take the largest group as the confident label.

Grouping (Option C = hybrid, transparent):
  1. Normalize each answer to stemmed content tokens (drop stopwords/garbage).
  2. Map tokens through a SMALL, explicit icon-synonym dictionary so known
     equivalents merge (search<->magnifier, close<->x<->cross, ...).
  3. Cluster answers that share >=1 canonical token (token-overlap).
  4. Confident label = the canonical token(s) of the largest cluster; its size
     over RUNS is the agreement/confidence.

The synonym map is intentionally tiny and visible so its bias is auditable.
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
        "this", "that", "appears", "to", "be", "represents", "and", "or"}

# Small, explicit synonym map -> canonical concept (auditable bias).
SYN = {
    "search": "search", "magnifier": "search", "magnifying": "search",
    "magnify": "search", "find": "search", "lookup": "search", "glass": "search",
    "close": "close", "x": "close", "cross": "close", "dismiss": "close",
    "card": "card", "credit": "card", "payment": "card", "debit": "card",
    "bill": "billing", "billing": "billing", "invoice": "billing",
    "document": "document", "doc": "document", "file": "document",
    "page": "document", "paper": "document",
    "gear": "settings", "settings": "settings", "cog": "settings",
    "chart": "chart", "graph": "chart", "bar": "chart", "analytics": "chart",
    "mail": "email", "email": "email", "envelope": "email", "message": "email",
    "chat": "chat", "bubble": "chat", "speech": "chat",
    "home": "home", "house": "home",
    "user": "user", "profile": "user", "avatar": "user", "account": "user",
}


def stem(w):
    for suf in ("ing", "ers", "er", "es", "s"):
        if len(w) > 4 and w.endswith(suf):
            return w[: -len(suf)]
    return w


def canon_tokens(text):
    t = text.lower()
    if any(m in t for m in ("<svg", "```", "viewbox", "{", "http", "path fill")):
        return set()
    raw = re.findall(r"[a-z]+", t)
    # Discard rambling / non-label outputs: a real icon name is a few words.
    # A long output is the model refusing/describing, not naming — reject it so
    # it cannot leak spurious tokens into the vote.
    if len(raw) > 6:
        return set()
    toks = [w for w in raw if w not in STOP]
    canon = set()
    for w in toks:
        w2 = SYN.get(w) or SYN.get(stem(w)) or stem(w)
        canon.add(w2)
    return canon


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


def vote(token_sets):
    """Largest cluster by shared canonical token: count how many runs contain
    each canonical token; the top token(s) and their count is the vote."""
    tally = Counter()
    for ts in token_sets:
        for tok in ts:
            tally[tok] += 1
    if not tally:
        return "(no answer)", 0
    top, k = tally.most_common(1)[0]
    return top, k


def main():
    agreements = []
    for stem_name, truth in ICONS:
        raws = [query(f"{OUT}/{stem_name}_base.png") for _ in range(RUNS)]
        token_sets = [canon_tokens(r) for r in raws]
        label, k = vote(token_sets)
        agreements.append(k)
        shown = " | ".join(" ".join(sorted(ts)) or "∅" for ts in token_sets)
        print(f"=== {stem_name} (truth: {truth}) ===")
        print(f"   voted={label!r} agree={k}/{RUNS}")
        print(f"   runs=[{shown}]\n")
    mean = sum(agreements) / len(agreements)
    print(f"mean agreement (with lexical grouping) = {mean:.2f}/{RUNS}  per_icon={agreements}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
