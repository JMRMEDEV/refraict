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

Follow-up (2026-09-02, from the Hermes stress test) — LOW-CONTRAST TEXT, not just
dark theme. Tesseract missed the "GO TO DASHBOARD" button on access-denied-dark:
light-teal text on a light-teal button (a low luminance-contrast pair, not a
dark-on-dark/light-on-dark inversion case). The auto-invert (item 1) does not
help because both fg and bg are mid-luminance. Consequences observed: (a) the
button text is absent from OCR/summaries, and (b) with no OCR text in that
region, the chart-label text-coverage gate (see 2026-09-02 fixes) cannot reject
gemma's spurious "chart" label there — the OCR miss propagates into a
element-label false positive. Candidate fixes: local contrast normalization
(CLAHE) or adaptive thresholding before OCR on low-contrast regions; or per-
region contrast-stretch using the measured fg/bg color pair. Deferred; the
residual is a single hard case, but it is the clearest remaining Gap 1 item.

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

### Gap 7 — Structural grounding: whole-image hypothesis cross-checked (RE-SCOPED; cross-check DONE, assembler out of scope)

Refraict emits correctly *positioned* components but does not *group* them into
the semantic containers a human/vision-LLM reads directly. On the Hermes
`board-dark` stress image, Refraict produced 103 correctly-placed components but
never assembled them into "4 named kanban columns (TO DO/IN PROGRESS/IN
REVIEW/DONE), each holding cards, each card having {label chip, assignee avatar,
checklist progress, comment count}". A vision-LLM reports that structure because
it reasons over the whole layout at once; a 3B VLM cannot, and asking it to emit
the hierarchy (the current `BuildGraphPrompt` path) is exactly the unreliable
step that hallucinates (and, before hardening, ran away — see 2026-09-02 stress
test).

The fix is HYBRID: a VLM whole-image structural hypothesis, VERIFIED and
corrected against deterministic measured evidence. This is refraict's founding
principle (AI + deterministic together), applied to structure. Neither half
alone suffices: geometry finds columns/cards but cannot name the container
("this is a kanban board with lanes"); the VLM names the container and its parts
but drifts on placement and counts.

Measured evidence (2026-09-02) — a SINGLE whole-image gemma3:4b call on the
1280px `board-dark` downsample recovered the structure the crop pipeline never
did: "Kanban board", all 4 columns by name (TO DO / IN PROGRESS / IN REVIEW /
DONE), card counts (TO DO 4 ✓, IN PROGRESS 3 ✓), and most card titles. BUT it
was ungrounded: it placed "Create dark mode color palette" in the wrong column,
miscounted IN REVIEW (5 vs 2), hallucinated "Team: Red Mars" as a section, and
mis-attributed the "3/5" checklist. So the VLM supplies a correct SCAFFOLD +
NAMING and wrong FACTS — exactly the split refraict is built to reconcile.

Recommended design (kanban as the first, highest-value pattern):
1. **VLM structural hypothesis (naming/scaffold).** Reuse the overview (`ov`)
   crop call the pipeline ALREADY makes; add a `--structure` prompt variant
   asking gemma for the container type + column names + rough card counts. One
   extra-cheap call, no new model resident. Treat the output as a HYPOTHESIS,
   never as fact.
2. **Deterministic verification/correction (placement/facts).** Column bands via
   x-center clustering anchored by the OCR'd header tokens ("TO DO (4)"); card
   rectangles from the OpenCV detector (Gap 3); assign each text/icon component
   to its enclosing card by containment and each card to a column by x-band.
   This CORRECTS the VLM's column membership and counts using measured geometry.
3. **Per-card attributes from evidence, not the VLM:** label chip = short
   uppercase token in a colored pill near the card top; assignee = 2-letter
   avatar token bottom-right; checklist progress = "N/M"; comment count = number
   adjacent to a speech-bubble icon (icon-labeler, Gap 6).
4. **Grounding pass over the hypothesis.** Flag any VLM structural claim the
   geometry cannot support (a named column with no matching x-band, a card count
   that disagrees with detected cards) — the same treatment colors get today.
   Emit a typed, provenance-tagged IR node (kanban_board → column[name,count] →
   card[label,assignee,checklist,comments]), each field carrying source =
   {vlm_hypothesis | measured | reconciled} and confidence.

Architecture note (owner's steer, 2026-09-02): refraict was always intended as
hybrid (AI + deterministic). A promising, lower-risk first step before a full
`internal/assemble` package is a **cross-check / double-check pass**: run the
existing whole-image (overview) VLM summary AND the crop-derived evidence, then
COMPARE them programmatically — where the whole-image structural claims and the
crop/OCR/region evidence agree, confidence is high; where they diverge, flag it
(and prefer the measured side). This reuses artifacts the pipeline already
produces (the `ov` crop description vs. the merged components / region summaries)
and turns the two independent reads into a mutual grounding signal, rather than
letting the text-model page summary be the single unchecked output. It is
effectively the grounding guard generalized from colors/text to STRUCTURE, and a
natural staging ground for the fuller hybrid assembler above.

Generalizes to nav sidebars, settings rows, and card grids via the same
containment+band approach.

SCOPE DECISION (2026-09-02, owner): the deterministic container assembler —
kanban/nav/settings-specific IR node types, column-band clustering, card-
containment assignment, per-card attribute extraction — is OUT OF SCOPE. It is
app-domain-specific structural RECONSTRUCTION: it would bake into refraict a
model of what a "kanban card/column" is, be brittle across app layouts, and
overreach the thesis (refraict emits measurable facts + grounded interpretation;
reassembling those into a specific semantic hierarchy is reasoning the calling
agent — which holds the components, coordinates, OCR, and the cross-check signal
— is better placed to do). Same line drawn under "Not a gap" below.

Therefore Gap 7's deliverable is bounded and DONE-in-principle:
  (1) the cross-check/double-check pass — DONE (2026-09-02): grounds gemma's
      whole-image read against measured evidence, emits crosscheck.json.
  (2) OPTIONAL, in-scope: a `--structure` overview prompt variant that asks
      gemma for a container-type + column/card HYPOTHESIS (a grounded VLM read,
      like any other), surfaced and cross-checked — never assembled by refraict.
  The container assembler (former step 3) is explicitly NOT built here.

Net: for structure, refraict emits the grounded evidence + an optional grounded
whole-image hypothesis, cross-checked — and stops. The agent assembles the
kanban/nav/settings semantics from that.

### Not a gap — comparison/verification stays with the agent

An earlier idea to add a `compare`/`verify` command was considered and rejected.
Refraict's role is to extract trustworthy *facts* from a rendered screenshot;
the calling AI agent already holds the "expected" side (it wrote the code and
knows the requirements) and is the better reasoner for the actual comparison.
Building spec-matching/assertions into Refraict would be redundant, invite a
brittle "spec format", and cause scope creep. Refraict emits facts; the agent
owns the verdict.

## Prioritized order (highest leverage first)

### Completed milestones

1. Auto-invert + upscale OCR (Gap 1). ← DONE (2026-09-01)
2. Expand deterministic page-type/semantic hints from clean OCR (Gap 2). ← DONE (2026-09-01)
3. Connected-components / OpenCV non-text detector (Gap 3). ← DONE (2026-09-01)
4. Harden the grounding guard: color naming + numeric-claim + quoted-text checks
   (Gap 4). ← DONE (2026-09-01)
5. Visual element typing — Tier 1 deterministic (Gap 6). ← DONE (2026-09-02)
6. Visual element labeling — Tier 2 grounded VLM vote (Gap 6). ← DONE (2026-09-02)
7. Text-call hardening (num_predict + call timeout) (robustness). ← DONE (2026-09-02)
8. Page-type grounding + chart-label gate (Gaps 2/6). ← DONE (2026-09-02)
9. Deterministic page assembly (qwen removed from crop + page). ← DONE (2026-09-02)
10. Cross-check comparator (Gap 7 step 1). ← DONE (2026-09-02)
11. Gemma self-consolidation; qwen fully out of default path. ← DONE (2026-09-03)

### Gap analysis: refraict vs. vision-LLM (`fs_read`) — 2026-09-03

Measured on the 25-image Hermes stress test (12-image `fs_read` baseline for the
vision-LLM side). Five dimensions scored:

| Dimension                | fs_read  | refraict | Gap        |
| ---                      | ---      | ---      | ---        |
| Text extraction          | 9/10     | 9/10     | ~parity    |
| Color accuracy           | 7/10 est | 10/10 px | refraict ↑ |
| Geometry / positioning   | 0/10     | 10/10    | refraict ↑ |
| Structural assembly      | 9/10     | 3/10     | LARGE      |
| Semantic interpretation  | 9/10     | 5/10     | MEDIUM     |

refraict surpasses a direct vision-LLM on measured FACTS (colors, coordinates,
exhaustive component lists) — the data gap is closed and inverted. The remaining
gap is INTERPRETATION: structural assembly (flat component list → grouped
containers with parent/child/sibling) and semantic intent (page purpose, element
implications, cross-element reasoning). The assembly gap is the one deliberately
scoped to the calling agent (Gap 7 re-scope); the milestones below narrow both
gaps by emitting richer STRUCTURED SIGNALS that make the agent's job trivially
easier, without refraict crossing the "emit evidence, don't assemble" boundary.

### Next milestones (narrowing structural + semantic gaps)

Ordered by leverage × simplicity, all deterministic, no new model:

**Milestone A — Containment edges** (structural assembly 3→6)
Priority: HIGHEST. Effort: small (one loop over component pairs).
For every component pair where A's bbox fully encloses B's bbox, emit
`{A contains B}` in graph.json. Currently graph.Build does spatial adjacency
(left-of, above) but NOT containment. Adding it gives the agent
"this card-panel contains these 5 text/icon components" directly. The
card→contents nesting — the single biggest missing structural signal — falls out
from geometry alone. This is the one item that moves the score the most with the
least work.

**Milestone B — Repeating-structure detection** (structural assembly 6→7-8)
Priority: HIGH. Effort: medium (clustering pass).
Kanban columns, nav items, settings rows, card grids all share a pattern: N
visually-similar regions at regular spacing along one axis. A clustering pass
over (width, height, x-center or y-center) of detected regions that tags groups
of same-sized, same-typed, regularly-spaced component clusters as a
`repeated_group` tells the agent "these 4 things are siblings in a list/grid."
On board-dark this turns "103 unrelated components" into "4 groups of similar
cards at regular x-intervals" — columns read themselves. No model; pure geometry
+ component-type matching.

**Milestone C — Richer inferPageType with confidence + signals** (semantic 5→6-7)
Priority: HIGH. Effort: small (extends existing classifier).
Return a structured object instead of a bare string:
`{type: "task_detail", subtype: "overdue_task", signals: ["PH-123", "Overdue",
"CHECKLISTS"], confidence: 0.9}`. The signals field shows the agent WHY the
classification was made. Add types for common UI states that refraict currently
misses: "error_state" (Access Denied, 404, error), "empty_state" (No items, Get
started), "confirmation" (Success, Verified, email sent). All keyword/pattern-
based. Cheap, already half-exists.

**Milestone D — Semantic text-pattern hints** (semantic 7→7-8)
Priority: MEDIUM-HIGH. Effort: medium (pattern library, iterative).
Attach a `semantic_hint` field to components whose OCR text matches known UI
patterns: `(Overdue)` in a red pill → `{hint: "overdue_deadline"}`; `2/5` next
to a progress bar → `{hint: "completion_ratio", value: "2/5"}`; `you@example.com`
→ `{hint: "placeholder_email"}`; `feat/PH-123-implement-login-screen` →
`{hint: "git_branch_ref"}`. The agent immediately knows what these components
MEAN without parsing raw text. Same philosophy as the icon-labeler (evidence-
attached interpretation) applied to text patterns. Start with 5–10 high-value
patterns; grow iteratively.

**Milestone E — Section-header association** (structural assembly 7-8→8)
Priority: MEDIUM, fragile. Effort: medium.
OCR tokens that are visually distinct (taller bbox = larger font, uppercase,
different color from body text) sitting directly above a component cluster are
likely section/column headers. A heuristic that associates "TO DO (4)" at y=180
with the component cluster at y=200–800 in the same x-band gives the agent NAMED
groups. More UI-dependent than A/B (font-size thresholds vary), so lower
priority, but high value on standard UIs.

**Target after A+C (the quick wins):** structural ~6/10, semantic ~7/10 — a
meaningful jump from today's 3/5, achievable in a single session, no scope
boundary crossed.

**Target after A+B+C+D:** structural ~7-8/10, semantic ~7-8/10 — approaching
the usable minimum where a calling agent can reconstruct most standard UI
semantics from refraict's evidence without a paid vision read.

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

### 2026-09-02 — Gap 6 Tier 1: deterministic visual-element typing

Added `classifyRegion(RegionBox, RegionSignals)`: types detected regions as
`icon` (text-empty, <=48px, compact, no children), `logo` (text-empty, header
band, non-solid), plus `container`/`card`/`panel`/`image`/`region` fallbacks.
OCR-awareness via `ocrOverlapFrac`; `RegionComponents`/`RegionComponentsOpenCV`
now take OCR tokens (threaded through the pipeline). Unit-tested
(`typing_test.go`), build+vet clean both configs.

Chart typing — evaluated and REMOVED. A deterministic bar/axis column-projection
was implemented, then measured on the reference image:
- real chart region: peak column-ink 0.09, 0 runs (bars are short and sparse);
- a KPI card with large "$1.58" text: peak 0.37, 3 runs (tall glyph columns).
So the heuristic false-positived a text card as a chart and missed the real
chart. It was removed rather than tuned; chart identification is deferred to
Tier 2 (grounded VLM), which can recognize a chart visually.

Honest limitation: on the reference screenshot no `icon`/`logo` was emitted
end-to-end, because the region detector's `MinSidePx` filter drops small icons
upstream and the logo was not isolated as a clean box. The typing rules are
correct and unit-tested; surfacing icons/logos end-to-end needs a detector
retuning pass (retain smaller regions) — tracked as a follow-up.

### 2026-09-02 — Gap 6 detector retuning + Tier 2 VLM element labeling

Detector retuning (surface icons): added an icon size-band to the OpenCV
detector (small, roughly-square contours are kept even below the main size
band; no rectangularity requirement, since icon glyphs are non-rectangular).
`classifyRegion` now types icons by GEOMETRY ALONE (small + compact + no
children), not OCR-emptiness — OCR frequently misreads an icon glyph as a
phantom character (e.g. the magnifier OCR'd as "Q." giving 36% box overlap),
which previously suppressed genuine icons. Verified end-to-end: cv_region types
`{icon:10, card:6, region:3}` on the reference image, icons landing on the real
sidebar nav glyphs + close button with minimal noise.

Tier 2 grounded VLM labeling: `analysis.label_elements` (default on),
bounded by `analysis.max_element_labels`. Each graphic region (icon/logo/chart/
image) is cropped with context padding and given a short grounded label via the
VLM; a deterministic sanitizer (`sanitizeElementLabel`) rejects refusals,
verbose non-answers, code fences/tokens, and list junk, requiring an element
noun or a clean multi-word phrase. The label is attached to
`Component.Semantic` as inference (source `vlm_element_label`) with provenance.

Honest ceiling: a 3B VLM labeling ~24px icon crops is unreliable. On the
reference image, 4 of 10 graphic regions received good labels ("Search icon",
"Bar chart", "Settings gear icon", "Email icon"); the sanitizer dropped the
other 6 as noise. This deliberately trades recall for precision — deterministic
geometry/colors remain the source of truth, and labels are best-effort
inference the guard keeps clean. Bigger VLMs (or the opencv-only path with
larger crops) would raise recall; not pursued to preserve the local/cheap model.

### 2026-09-02 — Icon-label reliability investigation (PoCs; scripts in dev/vec-poc/)

Explored making the Tier-2 icon labels more reliable. Measured, not assumed:

- Repetition voting (run the VLM N times, group answers, take the mode) is a
  real, model-agnostic reliability win. Baseline crops + lexical grouping +
  over-long-run filter: ~4/5 icons correct at 10 runs, and the agreement level
  (e.g. 8/10 vs 4/10) is itself a usable confidence signal (withhold labels
  below a threshold). RECOMMENDED technique.

- Vectorization (vtracer color trace, bg-removed and bg-kept variants) → REJECTED.
  Averaged 4-run majority voting: baseline upscale was the MOST self-consistent
  (2.6/4); bg-removed hurt most (1.2/4). Crisp vector art is further from the
  VLM's training distribution than a natural-looking blur, and bg-removal strips
  context. Not worth the vtracer + ImageMagick deps.

- Heavy lexical infra (Snowball stemming + WordNet via wnram + react-icons
  vocabulary) → REJECTED. 10-run Go PoC: mean agreement 4.2/10 and 1/5 correct,
  WORSE than a tiny curated synonym map (6.0/10, 4/5). WordNet lacks UI-icon
  metaphors (magnifier≠search in a dictionary), stemming fragmented votes, and
  the broad vocab admitted noise tokens. Reverted the package + 22MB data + both
  deps.

- KEY FINDING — the right icon-name→concept source: icon libraries that ship
  per-icon keyword/tag metadata. Lucide `icons/*.json` has e.g. search.json
  tags=[find, scan, magnifier, magnifying glass, lens, locate, explore, ...] —
  a real, maintained, community-curated alias→concept map that captures exactly
  the UI metaphors WordNet missed. RECOMMENDED replacement for both WordNet and
  the hand map: build an alias→canonical dict from Lucide tags (~1600 icons) and
  canonicalize votes against it. (Not yet implemented.)

## Target end state

Refraict will not out-understand the vision read, but with items 1 and 3 it can
match it on presence/position/color/text — deterministically and for free — and
use the grounding guard to trigger a paid vision read only when uncertain. That
is the cost-saving cross-check pattern this tool is built to enable.

### 2026-09-02 — Icon color inversion for VLM labeling → REJECTED

Hypothesis: since dark-theme OCR needed inversion, inverting dark icon crops
before the VLM might also help. Tested with 10-run voting on 5 dark-bg icons,
normal vs pure-color-inverted crops (no bg removal / tracing — just a color
flip, keeping a natural raster image). Result: mean agreement dropped 5.0/10
(normal) → 3.6/10 (inverted); every icon was equal or worse inverted.

Why inversion helps OCR but hurts the VLM: Tesseract assumes dark-on-light and
breaks on light-on-dark (a hard algorithmic mismatch inversion fixes). The VLM
has no such assumption — it trained on both; inverting instead discards color
information and shifts hues unnaturally without addressing the real bottleneck
(too few pixels in a ~24px icon). Same lesson as the vectorization PoC:
preprocessing that aids human/OCR legibility does not aid a VLM whose ceiling is
information content and model capacity, not contrast. Not adopted.

### 2026-09-02 — `icons` subcommand + crop-framing fix (real regression found)

Added a first-class `refraict icons <image>` subcommand: detect + type non-text
elements and (optionally) vote-label them. `--dump-crops <dir>` writes the exact
crop fed to the VLM; `--no-label` skips the model for a fast, deterministic
crop-inspection loop.

Using `--dump-crops --no-label` to inspect the actual crops revealed a real bug
that earlier metric-only analysis had wrongly dismissed as "stochastic
variance": the element crop showed the VLM a **tiny speck** (icon ~5% of the
512px canvas). Cause: the crop path used `CropRegion(maxLong)`, which only
DOWNSCALES — a ~60px padded icon region was never upscaled and sat tiny in the
center. Fix: `imageproc.ElementCropPNG` now scales the crop so its longest side
fills the inner margin (upscaling small icons). Re-dumped crops show the icon
filling the frame; measured vote agreement rose on the recognizable icons:
search 3/10→7/10, x 5/10→8/10, matching the PoC's 7–9/10. Hard/ambiguous icons
(billing, docs) stay low — genuine 3B-VLM ceiling; the threshold withholds them.

Lesson: inspect the actual model input, don't reason only from output metrics.
Refactors: `imageproc.ElementCropPNG` + `PadBox` (exported), `voteRawLabels` +
`buildVisionBackendKeepAlive` (shared by analyze + icons).

### 2026-09-02 — Honest icon-ID accuracy + threshold raised to 0.7

The PoC's "4/5 icons correct" was measured on a favorably-selected set (search,
x, card, close — icons the 3B VLM handles well). Testing the icons that were NOT
in that set (Docs/Help/Pricing/Chat) with the same clean crops + voting showed
they fail: docs→"credit card" 6/10, pricing→"credit card" 3/10, chat→"panel top
close" 3/10, help→"question" 2/10 (mean 3.5/10, ~0-1 of 4 correct). The crops
are visually clean, so this is a genuine 3B model-capability limit, not framing.
Real overall icon-ID hit rate is ~2-4 of 9 recognizable icons — below the
cherry-picked 4/5.

Consequence: the 0.5 acceptance threshold was too permissive — docs voted
"credit card" at 6/10 would have been emitted as a CONFIDENT WRONG label.
Raised `element_label_threshold` default to 0.7. Measured at 0.7: accepts only
search/x/square-m (all correct), withholds docs/pricing/chat/send. Precision
over recall — a withheld label is honest; a confident-wrong one is dangerous for
a verification tool.

### 2026-09-02 — gemma3:4b adopted as default vision model

Evaluated gemma3:4b (3.3 GB, same VRAM class as llava-phi3) across the whole
pipeline, not just icons. Measured vs llava-phi3 on deep-seek-ui.png:

- Icon labeling (threshold 0.7): gemma3 accepted 6 confident labels at ~1.0
  agreement (search, mail, chart, speech×2) vs phi3's 2-3. Big lift — confirms
  the icon-ID ceiling was partly model capability.
- Crop descriptions: gemma3 grounded and specific (real OCR text + measured
  colors) where phi3 often rambled/garbled.
- Page summary: references real content, notes uncertainty honestly.
- Cost: ~6m48 vs phi3 ~5m (slower per call + more labels succeed), ~177MB RSS.

Adopted gemma3:4b as the default `vision.model`. Two honest caveats:
1. High agreement != correctness — gemma3 is so self-consistent it can be
   confidently WRONG (docs→"chart bar" 10/10, pricing→"search" 8/10 both cleared
   0.7). The agreement threshold guards consistency, not truth; genuinely hard
   icons remain a model limit.
2. gemma3 cites hex color codes in prose ("#252829"), which the grounding
   guard's numeric check mis-flagged. Fixed: hex codes (#RRGGBB / bare 6-hex
   tokens) are stripped before the numeric-claim check (colors are validated by
   the color path). Unit-tested.

### 2026-09-02 — Per-model output-profile layer (internal/modelprofile)

Model-reactive filters (verbosity cap, hex-in-numbers, garbage markers,
structured-output) had accumulated as hardcoded constants in shared code, each
added in response to whichever model was tested last (e.g. the hex-in-numbers
strip was added when switching to gemma3, which cites "#RRGGBB" in prose). This
made per-model tuning implicit and messy.

Introduced `internal/modelprofile`: a Profile struct + registry (default,
gemma3, llava-phi3, moondream) resolved by model-name substring, with an
optional per-model config override (`vision.profile`: max_label_words,
strip_hex_in_numbers, structured_output). `iconlabel.NewWithProfile` and
`guard.CheckGrounding(..., stripHex)` now consume the resolved profile instead
of constants; the vision backend gets `StructuredOutput` from it too. Filters
that are genuinely general keep sane defaults; model-specific tuning is now
explicit and visible. Unit-tested (resolution + filter behavior).

### 2026-09-02 — Hermes 25-image stress test (gemma3:4b + qwen2.5:3b, opencv build) + text-call hardening

Ran a full stress test vs. an `fs_read` vision-LLM baseline over 25 native
2560×2048 Hermes UI screenshots (auth flows, kanban, dashboards, settings, task
detail/comments/attachments; dark+light). opencv build, Tesseract OCR wrapper,
`label_elements` on. All 25 completed. Aggregates: mean 51 OCR tokens/image,
mean 28 components (5–103), mean text_support 0.99, mean color_support 0.91,
grounding-clean on 12/25; element-labeling fired (mean 2.6 voted labels/image,
12 on the dense boards).

Findings:
- **Text/OCR is at/near parity** with the direct vision read on clean screens
  (exact transcription of comments, filenames+sizes, invitation bodies,
  verification text). Dark-theme OCR fix holds up. Confirms Gap 1 is effectively
  closed for legible UI text.
- **Deterministic geometry/detection scales** with real complexity (board 103/61
  components). Grounding guard correctly flagged fabricated colors (e.g. a
  `#FFFFFF` claim on login-dark).
- **The small text model (qwen2.5:3b) is the weakest link, as designed-around
  but not eliminated.** ROLE CHECK (traced in code): gemma3:4b does ALL image
  description per crop (`BuildGroundedCropPrompt`) and the icon voting; qwen
  never sees pixels — it only condenses gemma's text (`RegionSummary`/
  `PageSummary`). Correct division of labor. But qwen still violates its
  "compress only" instruction: it mislabeled a comments tab and a task-detail
  modal as "login interface" (bleed from the "Implement login screen" task
  title) and dropped salient content (settings DANGER ZONE). Colors in prose are
  frequently wrong (the guard is what catches them). OCR also misread "32
  Members" → "52 Members".
- **No structural assembly** — the board's 103 components were never grouped into
  named columns/cards with per-card attributes. See new Gap 7.

Changes applied (this entry):
- **Text-call hardening (robustness bug).** `inferPageGraph` made an unbounded,
  un-timed text-model call; on `invite-dark` it ran away and burned the full
  10-min Ollama HTTP timeout (516s vs. ~50s for the other 24) before falling
  back to geometry. Fixed: added `TextRequest.MaxTokens` → Ollama
  `options.num_predict` (graph=512, region=512, page=768) and a per-call
  `Ollama.CallTimeout` (default 90s) so a runaway degrades to the deterministic
  fallback in seconds. Re-ran the same image cold: 516s → **30s**, identical 14
  components. Unit-tested (timeout degradation + num_predict wiring).
- **Removed the per-crop RegionSummary round-trip.** `crossRegionSummary` was
  asking qwen to "summarize" a single already-short gemma description — no
  compression (nothing to aggregate at one crop) and a hallucination/latency
  surface. Now passes gemma's grounded description through verbatim; the text
  model's aggregation role is applied once, at the page level. Test updated to
  the passthrough contract.

### 2026-09-02 — Page-type grounding + chart-label gate (from stress-test findings)

Two targeted fixes from the Hermes stress test, both deterministic (no model swap):

- **Page-type grounding (Gap 2)** — fixed the "page ABOUT X described as the page
  that IS X" failure (a task-detail view titled "Implement login screen" was
  summarized as a login page). (1) `inferPageType` now weights STRUCTURAL
  container signals (task-ID token `^[A-Z]{2,5}-\d+`, CHECKLISTS / DUE DATE /
  MEMBERS, kanban headers, DANGER ZONE, invitation) above content keywords, and
  added `task_detail`/`kanban`/`invite` types. (2) The page-summary prompt
  (bumped `page-summary-v2`) is now authoritative about the type and carries an
  explicit "distinguish a page ABOUT X from a page that IS X; do not reclassify
  from quoted content" instruction. (3) The page type is computed early
  (post-OCR) and fed into gemma's per-crop prompt too (vision cache bumped
  `vision-v2`) to counter the mislabel at the source. Verified: task-detail →
  "Task Detail" (was "Login Form"); settings-light → "Settings" and now surfaces
  the DANGER ZONE it previously dropped.

- **Chart-label gate (Gap 6)** — small VLMs confidently mislabel blocky graphics
  and text buttons as "bar chart" (the label is free-text from gemma; there is
  no deterministic chart TYPE — removed earlier as unreliable). Gated the
  accepted chart-family label behind TWO deterministic conditions, both required:
  (1) region not OCR-text-dominated (>10% token-area coverage → reject; primary,
  since text-glyph columns fool a naive bar projection), and (2)
  `imageproc.HasBarChartGeometry` (>=3 varied-height bars sharing a baseline in a
  not-wide-thin region). Eliminated the false "chart bar" on 4 of 5 affected
  regions (verify-email, task-detail, board-dark ×2, settings). Unit-tested
  (bar-geometry pass/reject, text-coverage fraction, label matcher).

  Residual (1 case): access-denied's "GO TO DASHBOARD" button survives because
  Tesseract missed its low-contrast text (see Gap 1 follow-up), so the region has
  zero OCR coverage AND its letterforms mimic bars — neither deterministic gate
  can reject it without reading the text. Not over-fit; the real fix is upstream
  OCR contrast handling (Gap 1).

### 2026-09-02 — Deterministic page assembly (qwen removed from the default page composer)

Extended the "aggregate, not summarize" principle to the page level. Previously
qwen2.5:3b wrote `page.md` via `PageSummary` (condensing the region texts) — the
weakest, most hallucination-prone step. Now `page.md` is composed
DETERMINISTICALLY by `summarize.AssemblePage`: gemma's whole-image (overview `ov`
crop) description first — the grounded "original summary" — followed by each
focused section's gemma description verbatim under a header, with the
deterministic `pageType` at the top. No text model on the default path.

Rationale: a straight concatenation of already-grounded, already-short gemma
descriptions has nothing for a summarizer to compress; invoking qwen only added
drift and a dependency. qwen `PageSummary` is now retained ONLY for the opt-in
cloud-escalation path, where a stronger backend does genuine cross-region
synthesis (the one case a text model earns its cost). Net effects: removes a
hallucination surface; the text model no longer needs to be resident for the
page step (lower memory / one fewer model swap with keep-alive off); and — as a
bonus — `page.md` now preserves BOTH the whole-image read and the per-section
reads side by side, which is exactly the raw material the Gap 7 cross-check pass
needs (two independent gemma reads to diff for a structural grounding signal).

Verified on the worst prior hallucinator (voirel-task-detail): `page.md` now
leads "This is a task detail screen" (was "Login Form") with the overview read,
then the verbatim sections. Timing unchanged (dominated by vision + element-vote
labeling, not the single page call). Honest tradeoff: raw OCR garbage in a crop
now passes through into its section verbatim (no summarizer to smooth it) — the
correct tradeoff (honest about measured evidence; fix is upstream OCR, not prose
laundering). Unit-tested (`AssemblePage`: overview leads, sections verbatim,
page-type label, empty case).

### 2026-09-02 — Cross-check comparator: overview VLM read vs. measured evidence (Gap 7 step 1)

Implemented the first step of the Gap 7 hybrid: `detect.CrossCheck` compares
gemma's whole-image (overview `ov`) description against the deterministic
measured evidence (OCR text, measured colors, detected components) and emits
`evidence/crosscheck.json` (+ a `crosscheck` field in page.json). It reuses the
grounding-guard primitives so the two reports stay consistent. Passive/
diagnostic: agreement score + unsupported-claim list; the agent decides whether
to trust page.md or fall back to a paid read. No model call. This turns the two
independent reads (overview pass vs. crop/measurement pipeline) into a mutual
grounding signal — the grounding guard generalized from a single summary to a
second read.

Empirical result across board/home/task images (the point of this first cut):
the score discriminates real overview hallucinations from accurate reads.
- board-dark 1.00, org-home-dark 1.00 — overview fully corroborated.
- login-dark 0.62 — overview invented light colors (#FFFFFF, #F8F9FA) on a dark
  login screen; measured palette rejected them.
- voirel-task-detail 0.78 — overview asserted "5 tasks" (checklist is 2/5) and a
  mis-parsed "123 is"; counts unbacked by components.
- board-light 0.88 / project-home-light 0.95 — the recurring unmeasured "red"
  color claim, flagged deterministically.

Text and color checks are clean and high-value. Known weakness: the COUNT check
is noisy — a `<number> <word>` regex catches OCR/parse artifacts ("123 is",
"1 jd") as bogus count claims. Flagging them is not strictly wrong (they are
unsupported), but the count sub-check should be tightened (noun whitelist) or
demoted to advisory before counts are relied on. Unit-tested (supported text/
color, unsupported color, count agree/overclaim, empty).

Follow-up (same day): count check TIGHTENED — only verifiable container/element
nouns (card/column/panel/section/icon/button/logo/image/chart/avatar) are
checked; any other noun is ignored, not flagged. This killed the OCR/parse
artifacts: count-claim flags across the full 25 dropped to 1 (a legitimate
check). Then measured agreement across all 25 (fresh cache):

  mean 0.93, median 1.00, range 0.60–1.00; 13/25 perfectly consistent.
  Buckets: <0.7 → 3 images, <0.8 → 3, <0.9 → 5, <0.95 → 10.

Findings: the 3 lowest (0.60–0.67) are all auth pages (login-dark/light,
invite-light) where gemma's overview invented colors absent from the measured
palette — exactly the pages to distrust the whole-image read on. Text agreement
is near-1.0 on 23/25 (drops only on board-dark/invite-dark, tracing to OCR noise
on dense/dark pages); failures are dominated by COLOR, mirroring the original
grounding-guard result (small VLMs cite colors loosely).

Threshold guidance (agent-side policy; refraict emits the breakdown, agent owns
the verdict): the score is bimodal — a clean 1.0 cluster and a color-driven tail.
Recommend flagging a possible direct-read fallback when `agreement < 0.80` OR
`text_support < 0.90`. That catches the 3 drifted auth pages + board-dark's OCR
issue without penalizing the 10 pages at 0.9–0.99 that are off only by a loose
color word. Do NOT use `consistent == true` (agreement == 1.0) as the bar — it
over-triggers (12/25) on trivial color nitpicks. Report already exposes separate
text/color/count sub-scores so the agent can weight text mismatches above color.

Next (Gap 7): the OPTIONAL `--structure` overview prompt (a grounded VLM
hypothesis, cross-checked) — the deterministic container assembler is OUT OF
SCOPE (see Gap 7 re-scope).

### 2026-09-03 — A/B: qwen page synthesis vs. deterministic assembly (validates the qwen drop)

Isolated the "was dropping qwen from the page composer correct?" question with a
controlled A/B: two binaries off the SAME HEAD (all other fixes held constant),
differing ONLY in the default page composer — `AssemblePage` (current) vs. qwen
`PageSummary` (old behavior). Run on multi-region pages where synthesis would
matter most (board-dark, org-home-dark, settings-light, project-home-light).

Result — nuanced, and it confirms the drop as the DEFAULT:
- qwen prose is more readable (grouped concepts, less raw-OCR noise) — a
  human-reader advantage.
- But every qwen synthesis "win" came paired with a fabrication or degradation:
  org-home — invented "the page is centered around a Telouri project" (it shows
  three equal initiatives) and self-classified the page "analytics" (wrong);
  settings — truncated the DANGER ZONE warning "Actions here cannot be easily
  undone" to "Cannot easily be undone" (fact-drift).
- The assembled version loses NO measured facts: everything qwen stated
  correctly is present in assembly, which additionally keeps the raw tokens and
  does not editorialize.

Verdict: for refraict's consumer (an AI agent), assembly is more faithful and
lossless, and the agent is the better synthesizer from complete facts. qwen's
only real edge is human-readable prose, bought with reduced faithfulness — not
refraict's job. Keeping qwen `PageSummary` available (escalation path / possible
opt-in) rather than deleting it was therefore correct: the capability is not
wrong, it is just not the right default. Possible future refinement (not built):
an `output.page_style: assembled|synthesized` toggle to let a human-facing use
opt into qwen prose, making the faithfulness/readability tradeoff explicit rather
than baked in.

### 2026-09-03 — Gemma self-consolidation; qwen removed from the default path

Steer (owner): the one remaining qwen call — `inferPageGraph` relationship
inference — made no sense (a language model re-deriving spatial relations from a
text list of coordinates, when the geometry is already measured; also the call
that ran away on invite-dark). The place text reasoning IS worth it is
synthesizing the assembled page (overview + per-section reads) into one coherent
narrative — and gemma can do that TEXT-ONLY (it's a general instruction model
that also takes images), so it needs no second model. Confirmed gemma answers
text-only prompts; context is a non-issue (consolidation input max ~850 tokens
vs. gemma's 128K trained / 4K default num_ctx).

Implemented:
- Dropped the qwen graph augmentation (`inferPageGraph(ctx, merged, nil)`) —
  graph.json is now purely deterministic geometry. qwen is now OUT of the
  default analyze path entirely (crop=gemma passthrough, page.md=deterministic
  AssemblePage, graph=deterministic). qwen PageSummary remains only for opt-in
  cloud escalation.
- Added a gemma TEXT-ONLY consolidation pass (`BuildConsolidatePrompt`, reusing
  the already-warm vision model): it reconciles gemma's own overview + section
  reads into one de-duplicated narrative, told to stay consistent with the
  overview (self-consistency). Written as a SEPARATE artifact
  `page-consolidated.md` (never replaces the faithful deterministic page.md) and
  cross-checked against measured evidence (`evidence/consolidation_check.json`).

Result — consolidation_check across all 25 (the point of the run):
  mean 0.952, median 0.972, range 0.70–1.00; 12/25 perfectly grounded; only 1
  below 0.8, 3 below 0.9.
Critical comparison vs. the overview read's own cross-check (same run):
  consolidation better=7 / worse=6 / equal=12, mean slightly UP (0.952 vs 0.939).
So gemma self-consolidation does NOT systematically hallucinate — it preserves
(and sometimes improves, by reconciling sections against the overview) the
grounding of its inputs. This is the sharp contrast with the earlier qwen A/B,
where cross-model synthesis degraded faithfulness (invented "Telouri focus",
truncated DANGER ZONE, fabricated colors). Flags are almost entirely the known
color-naming looseness (text near-perfect, e.g. settings-light 26/26,
org-home-light 30/30); worst case invite-dark 0.70 (auth-page color drift), which
the consolidation_check score correctly surfaces. Example win: settings-light
consolidation is both readable AND fully grounded (agree 1.00, text 33/33) — the
best of the three composers (qwen truncated the warning; assembly was verbose).

Verdict: good steer. One model for the whole run; a readable, grounded narrative
alongside the faithful assembly, each with its own grounding score; qwen out of
the default path. Consolidation quality is capped by the overview read (it
faithfully carries forward gemma's own overview errors, e.g. org-home's
"analytics" misclass — correct behavior, not a regression).

## References & third-party sources

Tools, libraries, datasets, and papers used across this work, with licenses
(verified for anything we embed/redistribute).

Runtime dependencies (in the shipped tool):
- bild (github.com/anthonynsimon/bild) — pure-Go image processing (edges,
  morphology, threshold). MIT.
- gocv (gocv.io/x/gocv) — OpenCV 4.x bindings, OPTIONAL behind `-tags opencv`
  (Canny + findContours for low-contrast region detection). Apache-2.0; requires
  system OpenCV 4.x (BSD/Apache). Not in the default static build.
- Ollama — local model runtime (VLM + text models). Optional; the deterministic
  pipeline runs without it.
- Tesseract — OCR engine, invoked via the external OCR wrapper. Apache-2.0.

Icon-label reliability investigation (PoC only; scripts in dev/vec-poc/):
- vtracer (visioncortex) — color raster→vector tracer. Evaluated for icon
  crisping, REJECTED (did not improve 3B-VLM labeling). MIT. Not adopted.
- WordNet via wnram (github.com/lloyd/wnram) — lexical database. Evaluated for
  synonym grouping, REJECTED (missed UI-icon metaphors). WordNet license
  (BSD-like). Reverted; not a dependency.
- Snowball stemmer (github.com/kljensen/snowball) — evaluated with WordNet,
  reverted. MIT. Not a dependency.
- react-icons — icon-name vocabulary source (evaluated). MIT. Not adopted (the
  broad vocabulary diluted grouping).
- Lucide (lucide-icons/lucide, lucide-static `tags.json`) — RECOMMENDED source
  for the icon-name→concept alias map (per-icon keyword/tag metadata).
  License: ISC (Lucide) + MIT for the Feather-derived subset — both permissive
  and allow embedding/redistribution provided the copyright + permission notice
  is included. If the TF-IDF Lucide map is productionized, bundle the Lucide ISC
  and Feather MIT notices (see THIRD_PARTY_LICENSES). ~1792 icons.

Techniques referenced:
- TF-IDF term weighting (Spärck Jones, 1972) — used to down-weight common/
  ambiguous icon-tag tokens; the winning refinement of the Lucide alias map.
- Suzuki–Abe border following (1985) — the algorithm behind OpenCV findContours
  (used via gocv, not reimplemented).
- Two-pass connected-components labeling (union-find) — implemented from the
  standard published method in internal/detect (pure-Go detector).
- Majority-vote / self-consistency over repeated model samples — the validated
  reliability technique for VLM element labeling (agreement = confidence).

Licensing note: everything embedded or shipped is permissively licensed
(MIT/ISC/Apache/BSD). The only attribution obligation for a productionized
Lucide map is including the Lucide ISC + Feather MIT notices.
