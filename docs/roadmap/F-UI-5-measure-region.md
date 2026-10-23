# F-UI-5 — DESIGN NOTE: targeted-region measured-color verb (lower the thin-element grounding floor)

**Status:** DESIGN (2026-10-23). Proposes a new bounded MCP verb `measure_region`.
**Classification:** closes the refraict-side half of **F-UI-5** (a measurement-floor
BOUNDARY, *not* a bug). Preserves the ADR-0016 measured-only MET integrity invariant.
**Owner decision 2026-10-18:** note + design first; this is a decided future improvement,
not urgent.

> **Integrity invariant this note is bound by (ADR-0016).** refraict's Layer-2 value is
> that it emits ONLY measured, grounded facts. No positive color-grounding claim may be
> emitted on evidence refraict could not actually measure with confidence. Low confidence
> MUST stay low → the verify_ui conductor routes it UNVERIFIABLE → human. The verb below
> lowers the floor by measuring the *right rectangle*, **never by relaxing a confidence
> threshold or fabricating a sample.** A genuinely mixed/antialiased region still returns
> low confidence / `!grounded`.

---

## 1. Reproduction (grounded, against the TASK-378 frame)

Fixture: the canonical TASK-378 RUN-2 frame
`hermes-pyromon/experiments/task-378-verify-ui/evidence/20261018-220909/verify-ui-frame.png`,
copied into the repo as a stable test fixture:

```
testdata/task378-checklist-bar.png   1280x720   sha256 14206961d1034231ad08…
```

A collapsed checklist card at 100% ("2/2") renders a SOLID EMERALD-GREEN, full-width
progress bar. A human sees it instantly; the fix (bar → `$success` green at 100%) was
objectively correct. refraict returned `UNMET ×3` in the live run.

### 1a. `refraict analyze` (deterministic path, no Ollama needed for this evidence)

```
$ refraict analyze testdata/task378-checklist-bar.png --output /tmp/t378 --no-summary
Analysis complete: /tmp/t378 (36.9s), 5 crops, 7 components.
```

The 7 synthesized components — **there is no bar/region component**:

```
card bbox=[12,12,1268,216]
text bbox=[34,32,82,51]    HRM- 123
text bbox=[41,63,79,72]    DESIGN
text bbox=[34,95,228,109]  Migrate database schema to v2
text bbox=[1070,125,1216,150]  —C—CSCSC(‘é«C        <- garbled OCR over the bar's right edge
text bbox=[34,173,115,186]  2/2 Apr25
text bbox=[1199,173,1238,182]  AR SC
```

### 1b. The measured color samples (`evidence/colors.json`)

```
#FFFFFF  bbox=[12,12,1268,216]      (card)
#FFFFFF  bbox=[34,32,82,51]
#BABDBD  bbox=[41,63,79,72]
#F7F7F7  bbox=[34,95,228,109]
#078A52  bbox=[1070,125,1216,150]   <- the ONLY green sample
#FFDAD6  bbox=[34,173,115,186]
#C0C8C8  bbox=[1199,173,1238,182]
```

The single green sample `#078A52` (rgb 7,138,82) is **an accident of OCR**: it is attached
to the garbled text token at `[1070,125,1216,150]` (146×25) that Tesseract hallucinated over
the bar's right edge near the "2/2" counter. **It does not represent the bar** — the bar is
~1184px wide; this sample is 146px wide and sits at the wrong x-origin.

### 1c. The bar IS a clean, solid, measurable color when the right rect is targeted

A pixel probe (temporary `imageproc` test, since removed) scanning rows 120–160 for
green-dominant pixels across x=30..1220:

```
row y=127 green_px=1182
row y=128..141 green_px=1184..1186   (saturated across the full width)
row y=142 green_px=1182
green band rows y0=127 y1=142  height=16
SampleRegion(40,127,1210,143) => hex=#078A52 rgb=(7,138,82) ok=true
```

So the band is **~16px tall, full-width (x≈32..1216), solid `#078A52`**, and
`imageproc.SampleRegion` reads it perfectly **at native resolution** when pointed at the
correct rectangle. **The measurement is reliable; the DETECTION is the gap.**

---

## 2. Root cause (which stage drops the thin element)

Three hypotheses were on the table (handoff §WHY). Verified empirically:

| # | Hypothesis | Verdict | Evidence |
|---|---|---|---|
| **1** | The bar is never emitted as its own component/color sample (detection area/band floor) → `nearestColor()` has nothing to match. | **PRIMARY CAUSE — confirmed** | §1a: 0 bar components; §1b: no bar-shaped color sample (only the accidental OCR one). |
| **2** | Even if present, IoU between a ~1184×16 bar and coarser color-region bboxes is near zero → `nearestColor()` returns nothing / the container color. | **SECONDARY — would also bite** | §1b: the one green sample has a 146×25 bbox at the wrong origin; IoU vs the true bar rect ≈ 0.12. A bar-color query would mis-ground or fail. |
| **3** | Downscale/sampling averages the bar with its background → desaturated read → fails any "is this green" check. | **REJECTED** | §1c: native-resolution `SampleRegion` returns exactly `#078A52 ok=true`. No desaturation. |

### The detection floor, precisely (`internal/detect/regions_opencv.go`)

`DefaultOpenCVRegionOptions`: `MinSidePx: 24`, `IconMaxSidePx: 44`, `IconMaxAspect: 1.6`.
A contour is kept only if it passes the **main band** (BOTH width ≥24 **and height ≥24**)
**or** the **icon band** (roughly square — max/min aspect ≤ 1.6 — and max side ≤ 44px).

The bar is **~1184×16**:
- Main band: fails on **height 16 < 24** (too short for a card/panel).
- Icon band: fails on **aspect ≈ 74:1 > 1.6** and **max side 1184 > 44** (too wide/elongated).

It falls *between* the two bands — and the aspect gate is deliberately there to "reject wide
text-line contours." So a thin full-width bar is structurally un-detectable by the region
detector **by design**. This is exactly why F-UI-5 is a BOUNDARY, not a tuning bug: naively
lowering `MinSidePx` or widening `IconMaxAspect` to catch this bar would re-admit every
text-line and divider contour as a "region," degrading detection globally and manufacturing
low-quality samples — the opposite of the integrity posture.

**Conclusion:** the fix must NOT loosen the global detector. It must give the caller a way to
measure **exactly the rectangle the acceptance criterion is about**, with an honest
confidence — bypassing "whatever happened to get detected."

---

## 3. Proposed PRIMARY close — `measure_region` (new bounded MCP verb)

A new tool in `cmd/refraict-mcp`, registered alongside `get_components` / `query_text` /
`get_container_children`, following `query.go` conventions exactly (sandboxed `output_dir`,
bounded output, jsonschema-documented args).

### Contract

**Input**
```jsonc
{
  "output_dir": "<the output_dir a prior analyze returned>",  // required; sandbox-guarded
  "bbox": { "x0": 32, "y0": 127, "x1": 1216, "y1": 143 },     // required; the rect to measure
  "border_pct": 0.0,        // optional; fraction trimmed from each edge (default 0.0)
  "expect_hex": "#078A52",  // optional; if set, also report distance + within-tolerance
  "tolerance": 24           // optional RGB distance for expect_hex match (default 24)
}
```

The `bbox` is in **global image coordinates** — the same frame refraict's own `bbox_global`
color samples use. The caller (the capture driver, which knows the testid's rendered rect, or
a human/agent reading the frame) supplies the element's rectangle. `measure_region` resolves
the source image from `<output_dir>/manifest.json` (`image` + `image_sha256`, written by
`internal/cli/analyze.go`), loads it with `imageproc.Load`, and samples the rect directly.

**Output** (bounded packet — same byte/line discipline as every query verb)
```jsonc
{
  "hex": "#078A52",
  "rgb": [7, 138, 82],
  "coverage": 0.992,        // fraction of sampled pixels close to the dominant color
  "confidence": 0.95,       // measured confidence (see §4) — NOT a fixed pass flag
  "grounded": true,         // confidence >= groundedFloor AND bbox measurable
  "pixels_sampled": 18944,
  "bbox": { "x0": 32, "y0": 127, "x1": 1216, "y1": 143 },
  // present only when expect_hex was supplied:
  "expect": { "hex": "#078A52", "distance": 0, "within_tolerance": true }
}
```

### Why this is the right shape

- It measures the **exact element** the criterion is about, not an IoU match against whatever
  the detector emitted — so it closes BOTH root-cause #1 (no sample) and #2 (bad IoU) at once.
- It reuses the **proven measured-color pipeline** (`imageproc.SampleRegion`, native
  resolution — the path §1c proved reliable). No new CV model, no new heavy dep.
- It is a pure *read* of the on-disk analyze image artifact + manifest. No re-analyze, no
  Ollama, fast, and bounded. Stays inside the G-D context budget (a tiny fixed packet, never
  an artifact).
- The capture driver already knows each testid's `getBoundingClientRect()`; passing that rect
  to `measure_region` is the natural handshake (see §6 on the pyromon seam).

---

## 4. Integrity preservation — the confidence + coverage are REAL, not rubber-stamps

This is the crux. `imageproc.SampleRegion` today returns only the **median** color — for a
mixed/antialiased region the median is still *a* single color, which would be a false
"grounded" signal if returned naively. So `measure_region` MUST add a **coverage** computation
and derive confidence from it:

- **coverage** = fraction of interior pixels whose RGB distance to the sampled dominant
  (median) color is within a small intra-region threshold. A solid fill → coverage ≈ 1.0.
  An antialiased edge, gradient, or two-tone region → coverage well below 1.0 (pixels scatter
  across colors, none dominates).
- **confidence** is a monotonic function of coverage (and is clamped low when the rect is
  degenerate/tiny or escapes bounds). It is NOT a fixed constant and NOT tied to element size.
- **grounded** = `confidence >= groundedFloor`. A genuinely mixed region returns
  `grounded: false`, low confidence → the conductor keeps it UNVERIFIABLE → human.

This is what preserves the invariant: **the floor is lowered by measuring the correct rect,
not by lowering the bar for what counts as "measured."** A thin SOLID bar now grounds (it
genuinely is one measurable color); a thin AMBIGUOUS strip (gradient, antialiased 1px
underline over varied background, mixed chip) still does NOT ground. The control test in §7
proves exactly this asymmetry.

Explicitly NOT done (would regress the headline property):
- No lowering of `MinSidePx` / `MinAreaFrac` / widening `IconMaxAspect` in the global detector.
- No widening of `nearestColor` tolerance or `query_text` `color_tol`.
- No "if the element is small, assume the dominant color" shortcut.
- No fabricated color sample injected into `colors.json`.

`measure_region` adds a NEW, explicitly-targeted measurement path; it changes none of the
existing gates for any other element class.

---

## 5. What stays the same (scope discipline)

- `analyze`, `get_components`, `query_text`, `get_container_children`, `inspect` — unchanged.
- `get_artifact` stays **DENIED** on the Hermes mount (G-D). `measure_region` returns a tiny
  bounded packet, so it is allowlist-eligible exactly like the other query verbs.
- Detection, tiers, weights, grounding guard — unchanged. The thin bar still (correctly) does
  not appear as an auto-detected component; the conductor reaches it via `measure_region` with
  a caller-supplied rect.

---

## 6. SECONDARY / complementary path (owned by pyromon, NOT refraict)

ADR-0016 **decision 5** defines an ADVISORY `kiro_image_read`: an interpretive native
image-read attached to an UNVERIFIABLE→human verdict, to automate the operator's frame-read.
That is **pyromon's to build, not refraict's**, and it must **NEVER auto-MET** — interpretive
vision has no MEASURED Layer-2, so under that fallback a visual criterion can only reach
UNVERIFIABLE→human. `measure_region` is the complementary MEASURED path: it is the only one of
the two that can legitimately raise a thin-element criterion to a Layer-2 grounded fact (and
hence contribute to an autonomous MET). This note does not duplicate the advisory path.

### Where the rect comes from (a pyromon-side handshake, flagged honestly)

`measure_region` needs the element's rectangle. The cleanest source is the **capture driver**
(cypress), which already knows each testid's rendered rect via `getBoundingClientRect()`. So
the full close is two halves:

1. **refraict (this note):** the `measure_region` verb that, given a rect + `output_dir`,
   returns a measured `{hex, rgb, coverage, confidence, grounded}`.
2. **pyromon (separate, hand this back):** pass the known testid rect from the cypress capture
   into the `ui_analyze`/verify_ui Layer-2 step so it can call `measure_region` with it. If
   the rect is not threaded through, an agent/human can still supply it, but the turnkey close
   wants the driver to pass it.

Half (2) is a pyromon/verify_ui wiring change (the `ui_analyze@node` plugin + the driver→
analyzer rect handshake), not a refraict change. **Flagging it here so the refraict verb is
not asked to guess a rect** — refraict must never heuristically "find the probably-the-bar
rectangle," because that would re-introduce exactly the un-grounded guessing the invariant
forbids. refraict measures a rect it is GIVEN; it does not invent one.

---

## 7. Acceptance / test plan (for the implementation task)

Mirror `query_test.go` style (`context.Background()`, direct handler calls). All run against
the committed `testdata/task378-checklist-bar.png` so they need no Ollama and no network.

1. **Grounded case (the TASK-378 bar):** `measure_region` on the bar rect
   `{32,127,1216,143}` → `hex≈#078A52`, `rgb≈[7,138,82]`, `coverage` high, `confidence` high,
   `grounded: true`; with `expect_hex:"#078A52"` → `within_tolerance: true`. This is the
   before→after: the bar is now GROUNDED as green.
2. **!grounded antialiased/mixed control (integrity):** `measure_region` on a deliberately
   mixed rect (e.g. a card-edge/text region spanning fill + background, or a 1px band straddling
   the bar boundary) → `coverage` low, `confidence` low, `grounded: false`. Proves the verb does
   NOT falsely ground an ambiguous region — the invariant holds.
3. **Sandbox-escape guard:** `output_dir` + a bbox is fine, but a crafted `output_dir`/manifest
   `image` that resolves outside the sandbox (`../../etc/...`) is REJECTED by the `within()`
   guard — same guard the other verbs use. (Also: reject a bbox outside image bounds / degenerate.)
4. **Missing-artifact guard:** a nonexistent `output_dir`, or one without `manifest.json`,
   returns an actionable error, not a panic / not a false measurement.

`go build ./... && go test ./...` must stay green; `go vet ./...` clean.

---

## 8. Build order

1. `measure_region` handler in `cmd/refraict-mcp/measure.go` (manifest resolve → `imageproc.Load`
   → sample rect → coverage/confidence) + register in `main.go`.
2. A `SampleRegionCoverage`-style helper in `internal/imageproc` (median + coverage) so the
   coverage math is unit-tested where the sampling lives and reused, not duplicated in the MCP.
3. Tests per §7.
4. README MCP-tools table + tool description updated.
5. Commit on a marked branch (`feat/F-UI-5/measure-region`), Conventional Commits, **no push**.

---

## 9. One-line summary for the GAPS ledger

> **F-UI-5 refraict-side close (designed):** new bounded `measure_region` MCP verb samples the
> DOMINANT measured color of a caller-supplied rect directly from the analyze image artifact,
> returning `{hex, rgb, coverage, confidence, grounded}`. Lowers the thin-element grounding
> floor by measuring the RIGHT rect (not by relaxing confidence); a mixed/antialiased region
> still returns low confidence / `!grounded`, preserving the ADR-0016 measured-only MET
> invariant. The driver→analyzer rect handshake + the advisory `kiro_image_read` fallback are
> pyromon-side (handed back).
