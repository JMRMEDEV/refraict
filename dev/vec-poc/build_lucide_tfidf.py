#!/usr/bin/env python3
"""Build a TF-IDF-weighted Lucide alias->concept map (technique 1).

Rationale for the fix: the crude "shortest name wins" tiebreak sent "card" to
"gpu". With TF-IDF, a tag that appears on MANY icons (low IDF, e.g. "card",
"arrow") is a weak, ambiguous signal; a tag on FEW icons (high IDF) is
distinctive. We resolve each alias to the icon where it is most distinctive, and
we FLAG low-IDF aliases as weak so they do not confidently grab a niche icon.

Priority (unchanged intent, TF-IDF-refined):
  1. alias == icon name        -> itself (strong)
  2. alias is a name word      -> the containing concept (strong)
  3. alias is only a tag       -> the icon where it is most distinctive
                                  (highest IDF-normalized weight); weak if the
                                  tag's IDF is below a threshold.

Output: dev/vec-poc/lucide/alias_map_tfidf.json
  { "alias": {"concept": "...", "weak": bool, "idf": float}, ... }
"""
import json
import math
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
    N = len(tags)

    # Document frequency of each tag TOKEN across icons (treat each icon's tag
    # set + name words as its "document" of concept tokens).
    df = {}
    icon_tokens = {}
    for name, taglist in tags.items():
        toks = set(norm(name).split())
        for t in taglist:
            for w in norm(t).split():
                toks.add(w)
        icon_tokens[name] = toks
        for w in toks:
            df[w] = df.get(w, 0) + 1

    def idf(w):
        return math.log(N / (1 + df.get(w, 0)))

    # Weak threshold: tokens appearing on > ~1% of icons are ambiguous/common.
    weak_df = max(5, int(0.01 * N))

    # Build alias map with priority + TF-IDF resolution for tag-only aliases.
    # value: {concept, weak, idf, kind}
    alias = {}
    NAME, NAMEWORD, TAG = 3, 2, 1

    def better(new_kind, new_concept, cur):
        if cur is None:
            return True
        if new_kind != cur["kind"]:
            return new_kind > cur["kind"]
        # same kind -> prefer the SHORTER/more-generic concept name. (Using IDF
        # to pick the icon over-favored rare/niche icons, e.g. gear->wifi cog.)
        return len(new_concept) < len(cur["len_ref"])

    def add(a, concept, kind):
        a = norm(a)
        if not a:
            return
        w = idf(a.split()[0]) if a else 0.0
        cur = alias.get(a)
        if better(kind, concept, cur):
            alias[a] = {"concept": concept, "idf": round(w, 3),
                        "weak": (df.get(a.split()[0], 0) > weak_df and kind == TAG),
                        "kind": kind, "len_ref": concept}

    for name in tags:
        c = norm(name)
        add(c, c, NAME)
    for name in tags:
        c = norm(name)
        for w in c.split():
            add(w, c, NAMEWORD)
    for name, taglist in tags.items():
        c = norm(name)
        for t in taglist:
            tn = norm(t)
            add(tn, c, TAG)
            for w in tn.split():
                add(w, c, TAG)

    # Strip internal 'kind' before saving.
    out = {a: {k: v for k, v in d.items() if k not in ("kind", "len_ref")} for a, d in alias.items()}
    with open(f"{OUT}/alias_map_tfidf.json", "w") as f:
        json.dump(out, f)

    print(f"icons: {N}  aliases: {len(out)}  weak_df_threshold: {weak_df}")
    for probe in ["magnifier", "magnifying glass", "search", "x", "cross",
                  "close", "gear", "settings", "envelope", "mail", "card",
                  "credit card", "trash", "chart", "document", "file", "gpu"]:
        d = out.get(norm(probe))
        print(f"  {probe:18} -> {d['concept']!r} weak={d['weak']} idf={d['idf']}" if d else f"  {probe:18} -> (none)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
