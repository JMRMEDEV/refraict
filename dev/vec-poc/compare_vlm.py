#!/usr/bin/env python3
"""Compare VLM icon labels: baseline (blurry upscale) vs vectorized (crisp).

Sends each icon's _base.png and _vec.png to the local Ollama VLM with the same
grounded element prompt, prints labels side by side against ground truth.
"""
import base64
import json
import subprocess
import sys

OUT = "e2e-test/vec-poc"
MODEL = "llava-phi3"
PROMPT = ("You are shown a single cropped icon from a user interface. In a few "
          "words, name what this icon is or represents (e.g. 'search icon', "
          "'settings gear', 'credit card'). Reply with a short phrase only.")

ICONS = [
    ("search", "magnifier / search"),
    ("usage_bar", "credit card (Top up) [crop mislabeled]"),
    ("billing", "billing / document"),
    ("close_x", "close X"),
    ("topup", "top up / card"),
]


def label(path):
    with open(path, "rb") as f:
        b64 = base64.b64encode(f.read()).decode()
    req = json.dumps({"model": MODEL, "prompt": PROMPT, "images": [b64],
                      "stream": False, "keep_alive": "30s"})
    p = subprocess.run(["curl", "-s", "-m", "120",
                        "http://localhost:11434/api/generate", "-d", req],
                       capture_output=True, text=True)
    try:
        return " ".join(json.loads(p.stdout).get("response", "").split())[:160]
    except Exception:
        return "(error)"


def main():
    print(f"{'icon':10} {'truth':32}")
    for stem, truth in ICONS:
        b = label(f"{OUT}/{stem}_base.png")
        v = label(f"{OUT}/{stem}_vec.png")
        vb = label(f"{OUT}/{stem}_vecbg.png")
        print(f"\n=== {stem} (truth: {truth}) ===")
        print(f"  baseline           : {b}")
        print(f"  vectorized(bg-removed): {v}")
        print(f"  vectorized(bg-kept)   : {vb}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
