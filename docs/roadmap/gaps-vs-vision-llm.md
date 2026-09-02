# Refraict vs. Vision-LLM (`fs_read`): Gap Roadmap

Status: living document
Purpose: track what separates Refraict (local, deterministic, zero-cost UI
screenshot analyzer) from a built-in vision-LLM image read, and the prioritized
plan to narrow each gap. The strategic goal is NOT to out-understand a large
multimodal model, but to match it on *what is present, where, what color, and
what text* — deterministically and for free — and fall back to a paid vision
read only when the deterministic layer is uncertain.

## Reference test

- Image: `e2e-test/deep-seek-ui.png`
- Ground truth: a DeepSeek API platform **usage/billing dashboard**, dark theme.
  - Left nav: `Usage` (active), `API keys`, `Top up`, `Billing`; user `Josue Martinez`.
  - KPI cards: `Topped-up balance $0.41 USD`, `Total cost $1.58 USD`.
  - Stat cards: `Cost $1.58 USD`, `API requests 1,591`, `Tokens 20,998,307`.
  - Bar chart `Cost(USD) $1.58`, blue bars, x-axis 8/1–8/30, y-axis 0–0.8.

## Baseline three-way comparison (2026-09-01, 2x2 grid, llava-phi3 + qwen2.5:3b)

| Dimension | Built-in vision read | Refraict (grounded) |
|---|---|---|
| Semantic understanding | Correct: "DeepSeek billing dashboard" | Vague: "dark page with buttons and a table" |
| Text labels | Accurate | Heavily garbled ("SON", "Peee", "uso") |
| Colors | Qualitative ("dark") | Exact hex (#252829, #131415) |
| Positions/geometry | None | 30 components w/ pixel bboxes |
| Non-text elements | Sees chart, cards, buttons, avatar | Missed all (OCR-only components) |
| Cost | Paid per call | $0, local |
| Determinism | Stochastic | Deterministic (colors/geometry) |
| Speed | Seconds | ~35s local (2x2) |

## Gaps

### Gap 1 — Text accuracy (HIGHEST PRIORITY, mostly solvable)
Dark-theme UIs break Tesseract (light-on-dark; Tesseract expects dark-on-light).

Evidence (Tesseract on sidebar crop):
- original `--psm 6`: `"PTC lem nor PP ny OR)"`
- INVERTED `--psm 6`: `"Usage Q API keys Top up Billing"` ✅
- INVERTED `--psm 6` x2 on KPI: `"Topped-up balance ... Total cost $0.41 USD Top up $1.58 USD"` ✅ (USD correct)

Plan:
1. Auto-detect dark background (median luminance from measured colors) and
   invert before OCR.
2. Upscale small text ~2x before OCR.
3. Per-region invert: invert only dark regions using the existing color map for
   pages that mix light and dark areas.
4. If Tesseract plateaus: swap to PaddleOCR/RapidOCR (ONNX, CPU, free) via the
   existing `ExternalEngine` env-var hook (drop-in).

### Gap 2 — Semantic understanding (hardest, partially solvable)
A 3B VLM lacks world knowledge to name product/page intent.

Plan:
1. Better OCR (Gap 1) feeds better semantics — grounded labels like
   "Usage/Billing/Cost(USD)/Tokens" let even a small model infer "usage dashboard".
2. Expand deterministic `inferPageType` keyword sets (billing, usage, analytics).
3. Accept a residual gap; deep nuance stays with the paid vision read.

### Gap 3 — Non-text element detection (high value, solvable) — DONE (narrow case)

Implemented via bild (pure Go) + a self-written connected-components detector for
the default build, and an OpenCV Canny detector behind `-tags opencv` for the
hard low-contrast case. Wired into the pipeline behind `analysis.detect_regions`
(default true). See update log 2026-09-01.

Original plan (kept for reference):
Refraict currently derives components only from OCR text, so it misses charts,
cards, buttons, avatars, and other non-text UI.

Plan:
1. Deterministic connected-components / contour detection (edges, filled rects)
   to propose non-text region boxes — pure Go or small ONNX/OpenCV, no GPU.
2. Rule-based typing: box + text + rounded corners => button; box with repeated
   vertical bars => chart; large box enclosing others => card/panel.
3. This replicates OmniParser's one genuine strength (region detection) cheaply,
   without its cost/hardware profile.

### Gap 4 — Grounding-guard robustness (needs hardening)
The guard passed garbage ("Peee") because it was in the OCR corpus, and passed
`#161718` mislabeled as "brown" due to loose color tolerance.

Plan:
1. Clean OCR (Gap 1) removes most garbage-quoting.
2. Deterministic hex -> nearest named color; tighten tolerance so gray != brown.
3. Flag low-confidence OCR tokens so summaries can't quote them as fact.

### Gap 5 — Structured output the vision read cannot give (existing moat)
Exact hex colors, pixel-precise bboxes, spatial relationship graph, determinism,
and zero cost. Keep leaning into these; they are the reason the tool exists.

### Gap 6 — Visual element typing & labeling: icons, logos, charts (needed overall)

Refraict detects non-text regions (Gap 3) but does not yet say *what* they are:
icons, brand logos, and charts currently appear as anonymous boxes (or, for
text, as OCR tokens). Any thorough UI analysis needs these element categories.

Approach — split by how well it fits the deterministic/local/cheap identity:

Tier 1 — Deterministic typing (do first; fits perfectly):
- Icon detection: classify small, compact, non-text regions (~16–32px, low
  fill of OCR text) as `icon`. Uses existing region + OCR data; rule-based.
- Logo/image detection: larger non-text graphic regions (often header/top-left)
  typed as `image`/`logo` (position + size + OCR-emptiness heuristic).
- Chart detection: type a region as `chart` via a bar/axis pattern — regular
  vertical/horizontal filled runs (column/row projection) or axis-line detection.

Tier 2 — Grounded VLM labeling (reuses existing grounded-crop machinery, opt-in):
- For regions typed icon/logo/chart, run a short grounded VLM description on the
  sub-crop to get a human-usable label ("search icon", "brand wordmark",
  "bar chart, values trending up"). Marked as inference and passed through the
  grounding guard, exactly like crop/page descriptions.

Tier 3 — OUT OF SCOPE (do not build; departs from local/cheap):
- Brand/logo *recognition* ("this is the Stripe logo") — needs a brand database
  or a large model. The calling agent, which has brand context, is better placed.
- Reading precise chart data values ("bar 5 = 0.52") — fragile CV; defer or
  leave to a heavier external tool.

Plan: (1) deterministic region typing into icon/logo/chart; (2) grounded VLM
labeling of those typed regions behind the existing summary/guard flow.

### Not a gap — comparison/verification stays with the agent

An earlier idea to add a `compare`/`verify` command was considered and rejected.
Refraict's role is to extract trustworthy *facts* from a rendered screenshot;
the calling AI agent already holds the "expected" side (it wrote the code and
knows the requirements) and is the better reasoner for the actual comparison.
Building spec-matching/assertions into Refraict would be redundant, invite a
brittle "spec format", and cause scope creep. Refraict emits facts; the agent
owns the verdict.

## Prioritized order (highest leverage first)

1. Auto-invert + upscale OCR (Gap 1). ← DONE (2026-09-01)
2. Expand deterministic page-type/semantic hints from clean OCR (Gap 2). ← DONE (2026-09-01)
3. Connected-components / OpenCV non-text detector (Gap 3). ← DONE narrow case (2026-09-01)
4. Harden the grounding guard: color naming + numeric-claim + quoted-text checks
   (Gap 4). ← DONE (2026-09-01)
5. Visual element typing — Tier 1 deterministic: classify detected regions as
   icon / logo(image) / chart (Gap 6). ← NEXT
6. Visual element labeling — Tier 2 grounded VLM description of icon/logo/chart
   sub-crops, guard-checked (Gap 6).
7. Optional: PaddleOCR/RapidOCR swap if Tesseract plateaus (Gap 1). Residual
   Tesseract errors after invert+upscale: "usp"/"uso" (USD), "deepseck",
   "APli keys".

## Update log

### 2026-09-01 — Gap 1 OCR fix applied (auto-invert + 2x upscale)

Change: `e2e-test/tesseract-ocr.py` detects dark background via mean luminance,
inverts, upscales 2x, uses `--psm 6`, rescales bboxes to original coords.

Results (`results-v6`, 35s, ~100MB RSS): OCR garbage -> accurate (Topped-up
balance, $0.41, Usage, API keys, Billing, 1,591, 20,998,307, Josue Martinez);
components 30->40, tokens 66->81, all bboxes in-bounds; guard flagged "white"
vs dark palette. Refraict now matches vision read on text+color+position.

### 2026-09-01 — Deterministic gap batch (Gaps 4a, 4b, 2, 1-residual)

Changes:
- Gap 4a (`detect/guard.go`): named-color check uses NEAREST measured color;
  neutral (achromatic) colors support "gray" always plus black/white by
  luminance band, never "brown". Fixes prior mislabel of dark gray as brown.
- Gap 4b (`detect/guard.go`): numeric-claim check flags currency/number values
  in the summary absent from the OCR corpus (normalizes $, commas, trailing
  zeros; ignores trivial 0-9). New claim kind "number".
- Gap 2 (`cli/helpers.go`): `inferPageType` expanded with billing/usage/
  analytics/api keyword sets.
- Gap 1 residual (`e2e-test/tesseract-ocr.py`): conservative whole-token
  normalization allowlist (usp/uso->USD), preserves trailing punctuation.

Tests: detect suite 20 tests + 2 inferPageType tests, all pass; go vet clean.

Results (`results-v7`, 27.6s): guard CAUGHT a real error — summary said
`"Total Requests": 20,998` but OCR value is `20,998,307`; flagged as
`{kind: number, claim: "20,998"}`. Colors "black"/"gray" correctly supported
(color_support 1.0), `$1.58 USD` passed. 3 USD tokens, 0 usp/uso.

Not caught (by design, out of straightforward scope): phrase recombination
("Total Requests" as a label when the words appear separately), and semantic
mislabeling of non-text elements (Gap 3).

### 2026-09-01 — Gap 3 non-text region detection (narrow case)

Dependency decision: adopted OpenCV via `gocv.io/x/gocv` v0.28.0 (OpenCV 4.5.4),
isolated behind `//go:build opencv`. Default build stays pure-Go (bild v0.17.0)
and statically linkable; only `-tags opencv` pulls in CGo+OpenCV.

Why OpenCV: the test UI's cards are ~10% contrast (fill ~48 vs bg ~23 gray).
Pure-Go fill-threshold and single-threshold Sobel failed 3x (merged everything
or erased cards). OpenCV Canny hysteresis links faint broken borders into closed
contours — detected 9 clean regions where pure-Go got 1-2.

Detector: `internal/detect/regions.go` (pure-Go connected-components, two-pass
union-find written from the published method) and `regions_opencv.go`
(`//go:build opencv`: Canny -> dilate -> FindContours -> boundingRect ->
rectangularity filter -> IoU dedup (>=0.80, keep outer) -> nested filter (drop
>=90% contained)). Conservative typing: container/card/panel/region.

Pipeline wiring: `analysis.detect_regions` config flag (default true). Build-tag
dispatch `detectRegionComponents(img)` in `cli/regions_purego.go` (!opencv) and
`cli/regions_opencv.go` (opencv), both returning []ir.Component. Region
components are appended to cropComponents before dub.Reconcile, so they merge
with OCR/VLM components by IoU.

Verified end-to-end on deep-seek-ui.png:
- `-tags opencv`: 49 merged components = 40 ocr + 9 cv_region (2 KPI cards,
  3 stat cards, chart container, nav pill, 2 dropdowns). Cards and their inner
  text both survive reconciliation (distinct, low IoU). 55s, 177MB RSS.
- default pure-Go: 42 components = 40 ocr + 2 cv_region (graceful: fewer on this
  low-contrast image, no crash). 34s.
- Both build + vet clean; full test suite passes; default build statically
  linked (CGO_ENABLED=0). New tests: connected-components/detector (pure-Go) +
  IoU-dedup/nested-filter (opencv-tagged).

Remaining (medium case, deferred): tune pure-Go detector or accept OpenCV for
low-contrast; genuine child-element detection inside containers; chart bar
detection via projection.

## Target end state

Refraict will not out-understand the vision read, but with items 1 and 3 it can
match it on presence/position/color/text — deterministically and for free — and
use the grounding guard to trigger a paid vision read only when uncertain. That
is the cost-saving cross-check pattern this tool is built to enable.
