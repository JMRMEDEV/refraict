#!/usr/bin/env python3
"""Vectorization PoC for icon labeling.

For each known icon crop, produce two large PNGs for VLM comparison:
  (A) baseline  — plain bicubic upscale of the raw crop
  (B) vectorized — 3 stages: (1) background removal, (2) color trace via
      vtracer (keeps colors), (3) rasterize the SVG large via `convert`.

Outputs go to e2e-test/vec-poc/. Requires: Pillow, vtracer, ImageMagick convert.
"""
import os
import subprocess
import sys
from PIL import Image, ImageOps

SRC = "e2e-test/deep-seek-ui.png"
OUT = "e2e-test/vec-poc"
TARGET = 512  # large output side for the VLM

# Known icon crops (x0,y0,x1,y1) with ground-truth labels, padded a little.
ICONS = [
    ("search",   (20, 168, 56, 204), "magnifier / search (API keys)"),
    ("usage_bar",(19, 213, 56, 246), "bar chart (Usage)"),
    ("billing",  (20, 255, 56, 291), "billing / document"),
    ("close_x",  (1868, 4, 1900, 36), "close X"),
    ("topup",    (20, 213-0, 56, 246+0), "top up / card"),  # near usage; adjust below
]
# fix topup coords (its row ~ y=225 area for 'Top up')
ICONS[4] = ("topup", (20, 210, 56, 246), "top up")


def sh(cmd):
    return subprocess.run(cmd, capture_output=True, text=True)


def remove_dark_bg(im, thresh=70):
    """Stage 1: background removal. Dark background -> transparent; keep the
    lighter icon pixels with their color."""
    im = im.convert("RGBA")
    px = im.load()
    w, h = im.size
    for y in range(h):
        for x in range(w):
            r, g, b, a = px[x, y]
            lum = 0.299 * r + 0.587 * g + 0.114 * b
            if lum < thresh:
                px[x, y] = (r, g, b, 0)  # transparent bg
    return im


def baseline(crop, path):
    """(A) plain bicubic upscale."""
    im = crop.convert("RGB")
    scale = TARGET / max(im.size)
    big = im.resize((int(im.width * scale), int(im.height * scale)), Image.BICUBIC)
    # pad to square on white
    canvas = Image.new("RGB", (TARGET, TARGET), (255, 255, 255))
    canvas.paste(big, ((TARGET - big.width) // 2, (TARGET - big.height) // 2))
    canvas.save(path)


def vectorized(crop, stem):
    """(B) bg-remove -> color trace (vtracer) -> rasterize large."""
    # Stage 1: remove dark bg, composite icon on white so trace sees clean fg.
    nobg = remove_dark_bg(crop)
    white = Image.new("RGBA", nobg.size, (255, 255, 255, 255))
    comp = Image.alpha_composite(white, nobg).convert("RGB")
    # upscale a bit first so vtracer has more to trace (still real pixels)
    scale = 8
    comp = comp.resize((comp.width * scale, comp.height * scale), Image.NEAREST)
    pre = f"{OUT}/{stem}_pre.png"
    comp.save(pre)
    # Stage 2: color trace to SVG.
    svg = f"{OUT}/{stem}.svg"
    r = sh(["vtracer", "--input", pre, "--output", svg,
            "--colormode", "color", "--filter_speckle", "4",
            "--mode", "spline", "--hierarchical", "cutout"])
    if r.returncode != 0 or not os.path.exists(svg):
        return None, r.stderr
    # Stage 3: rasterize SVG large via ImageMagick.
    outpng = f"{OUT}/{stem}_vec.png"
    r2 = sh(["convert", "-background", "white", "-density", "300", svg,
             "-resize", f"{TARGET}x{TARGET}", outpng])
    if r2.returncode != 0 or not os.path.exists(outpng):
        return None, r2.stderr
    return outpng, ""


def vectorized_bg(crop, stem):
    """(C) color trace WITH background kept (no bg removal), rasterize large.
    Isolates whether background removal was what hurt VLM labeling."""
    comp = crop.convert("RGB")
    scale = 8
    comp = comp.resize((comp.width * scale, comp.height * scale), Image.NEAREST)
    pre = f"{OUT}/{stem}_prebg.png"
    comp.save(pre)
    svg = f"{OUT}/{stem}_bg.svg"
    r = sh(["vtracer", "--input", pre, "--output", svg,
            "--colormode", "color", "--filter_speckle", "4",
            "--mode", "spline", "--hierarchical", "stacked"])
    if r.returncode != 0 or not os.path.exists(svg):
        return None, r.stderr
    outpng = f"{OUT}/{stem}_vecbg.png"
    # keep the traced background (do not force white) — density then resize.
    r2 = sh(["convert", "-density", "300", svg,
             "-resize", f"{TARGET}x{TARGET}", outpng])
    if r2.returncode != 0 or not os.path.exists(outpng):
        return None, r2.stderr
    return outpng, ""


def main():
    os.makedirs(OUT, exist_ok=True)
    src = Image.open(SRC)
    manifest = []
    for stem, (x0, y0, x1, y1), truth in ICONS:
        crop = src.crop((x0, y0, x1, y1))
        base = f"{OUT}/{stem}_base.png"
        baseline(crop, base)
        vec, err = vectorized(crop, stem)
        vecbg, errbg = vectorized_bg(crop, stem)
        manifest.append((stem, truth, base, vec or "(failed)", vecbg or "(failed)"))
        print(f"{stem:10} truth={truth!r}")
        print(f"   baseline:      {base}")
        print(f"   vectorized(bg-removed): {vec if vec else '(FAILED) '+err[:100]}")
        print(f"   vectorized(bg-kept):    {vecbg if vecbg else '(FAILED) '+errbg[:100]}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
