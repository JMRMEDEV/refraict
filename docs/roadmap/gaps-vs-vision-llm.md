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

FINDING (2026-09-03): containment edges are ALREADY implemented in
`graph.relationship` (`a.BBox.Contains(b.BBox) && area>3x`). On board-dark this
produces 41 correct card→contents edges (9 cards, each nesting its label/title/
assignee text). So Milestone A's mechanism is DONE. The real bottleneck exposed
by measuring: containment only works where CARD REGIONS ARE DETECTED, and the
detector misses cards on light/low-contrast themes — board-dark finds 9 cards
(41 edges) but board-light finds 0 cards (3 edges). The structural-assembly
score is gated by DETECTION QUALITY on flat UIs, not by the containment logic.
Redirected to Milestone A2.

**Milestone A2 — Edge-enhancement preprocessing for card detection on flat UIs** (PARTIAL; capability added, default OFF pending adaptive gating)
Priority: HIGHEST (real bottleneck). Effort: medium (detector preprocessing).
The region detector keys on edge magnitude (Sobel pure-Go / Canny opencv); faint
hairline card borders on near-white backgrounds produce too weak a gradient to
threshold, so cards go undetected on light themes. Fix (owner steer): a
deterministic mid-processing contrast/edge-amplification step on the WORKING copy
fed to edge detection (not the original used for color sampling) — CLAHE / local
contrast normalization or unsharp-mask — so faint borders become detectable
gradients.

Implemented + measured (2026-09-03) — first attempt REJECTED, honest finding:
`RegionOptions.EdgeBoost` + `boostEdges` hook added (applied to a SEPARATE edge-
detection input; fill mask keeps unboosted gray; color/coords use the untouched
original). The naive body (bild global `adjust.Contrast` + `UnsharpMask`) was
measured and does NOT work:
- board-dark at boost 0.6 FRAGMENTED solid card borders (containers → header-band
  sub-parts).
- Worse, the unit test exposed the core flaw: global contrast expands values
  around mid-gray, so a faint light border (235 on a 250 background) AND the
  background BOTH saturate toward 255 — ERASING the difference. Global contrast
  is the wrong primitive for faint light borders.
So `boostEdges` is left a documented NO-OP and `EdgeBoost` defaults to 0 (zero
behavior change vs. base — verified identical output). Also learned: the earlier
"board-dark 9 cards" was the OPENCV Canny path; pure-Go finds 0 cards on
board-dark regardless (separate pure-Go weakness).

Correct fix (deferred): LOCAL contrast — CLAHE (tiled/adaptive histogram
equalization), which lifts faint local borders without saturating flat regions.
bild lacks it; it's a real implementation (tiled histogram + bilinear
interpolation of tile mappings), not a one-liner. That is the actual A2 task.

"Lean on opencv Canny" shortcut — EMPIRICALLY DISPROVEN (2026-09-03):
Tested the hypothesis that the opencv Canny hysteresis path already handles the
faint borders pure-Go misses. It does NOT. Measured board-light:
- opencv default (Canny 20/60): 0 cards (same as pure-Go). A threshold sweep
  (Canny down to 6/18, rectangularity down to 0.35, dilate up to 5) never
  surfaced a single CARD-sized contour — the ~19 boxes found are all icons +
  2 dark banner images; zero cards at any setting.
- Root cause pinned by pixel inspection: on board-light the card INTERIOR
  luminance (249) EQUALS the page background (249) — zero fill contrast — and the
  "border" is only a 9-luminance-unit step (249→239, ~3.5%). There is no closable
  card-sized contour to find at that faintness, so no Canny threshold recovers it;
  and global contrast pushes both 249 and 239 to 255 (the rejected boostEdges).
Conclusion: only LOCAL adaptive equalization (CLAHE), which stretches the local
249↔239 step into a strong edge while leaving it distinguishable from the flat
interior, can recover these cards. CLAHE-before-edge-detection is confirmed as
THE A2 approach; both the global-contrast boost and Canny-threshold tuning are
dead ends for zero-fill-contrast cards.

RESOLVED (2026-09-03) — DUAL-PASS opencv CLAHE:
Implemented via native gocv CLAHE. Single-pass CLAHE had a tradeoff (recovered
board-light cards 0→3 but WIPED task-detail icons 8→0, because CLAHE's local
amplification merges/erases small icon-band contours). A stddev gate was tried
and rejected — global stddev is the wrong signal (board-light 48 > board-dark 22;
local faintness ≠ low global contrast). Solution: `DetectRegionsOpenCV` now runs
TWO passes and unions them (IoU-dedupe, keep-pass-1-preferred):
  pass 1 (no CLAHE) → icons + high-contrast regions (icon-reliable);
  pass 2 (CLAHE clip=8, 8×8 tiles) → faint cards on flat UIs.
Measured (dual-pass): board-light cards 0→3 AND icons kept 17; board-dark
unchanged (9 cards, 18 icons); voirel-task-detail cards 1→2 AND icons kept 8
(single-pass had wiped them). End-to-end, board-light containment edges went
3→21 — the recovered cards now nest their contents (Milestone A works on light
themes). Best of both, no tradeoff. Note: recovers 3 of ~9 board-light cards
(the faintest 6 still don't close a contour even post-CLAHE) — partial but real;
further gains would need per-card local thresholding, diminishing returns.
Pure-Go path still lacks CLAHE (opencv-only); a hand-rolled CLAHE remains the
pure-Go follow-up. Unit-tested (boxIoU, unionRegionBoxes).

**Milestone B — Repeating-structure detection** (structural assembly ~5→7) — DONE (2026-09-03)
`graph.DetectRepeatingGroups` clusters same-typed, similarly-sized components by
axis center and tags groups of >=2 at regular spacing as `ir.RepeatedGroup`
(axis, spacing, type, member_ids), attached to graph.json `repeated_groups`.
Measured board-dark: recovers the kanban layout — 3 x-axis card groups (the
columns, spacing ~290-306) plus their y-axis vertical stacks. board-light: 2 card
groups; org-home-dark: the 3 project tiles as a y-column. A min-spacing filter
(<20px) drops degenerate overlapping-component groups. No model. Unit-tested.

Original plan:
Priority: HIGH. Effort: medium (clustering pass).
Kanban columns, nav items, settings rows, card grids all share a pattern: N
visually-similar regions at regular spacing along one axis. A clustering pass
over (width, height, x-center or y-center) of detected regions that tags groups
of same-sized, same-typed, regularly-spaced component clusters as a
`repeated_group` tells the agent "these 4 things are siblings in a list/grid."
On board-dark this turns "103 unrelated components" into "4 groups of similar
cards at regular x-intervals" — columns read themselves. No model; pure geometry
+ component-type matching. (Depends on A2 for light themes.)

**Milestone C — Richer inferPageType with confidence + signals** (semantic 5→6-7) — DONE (2026-09-03)
`inferPageType` now returns `ir.PageType{type, signals, confidence}` (exposed in
page.json `page_type`) instead of a bare string. Added types: error_state,
confirmation, verify_email, forgot_password, project_home. Verified fixes:
access-denied → error_state (was generic/dashboard); org-home → dashboard (was
analytics/settings); verify-email → verify_email (conf 0.75); forgot-password →
forgot_password. Known residual: project-home-light → kanban (its board NAMES
"Sprint Board"/"Backlog" trip the keywords — the "page about X" issue, milder);
the signals field surfaces the reason so the agent can judge.

**Milestone D — Semantic text-pattern hints** (semantic ~6.5→7-8) — DONE (2026-09-03)
`detect.AttachSemanticHints` attaches `ir.SemanticHint{kind,value}` to components
whose text matches a known UI pattern (task_id, git_branch_ref, email,
overdue_deadline, completion_ratio, currency_amount, percentage, count_badge).
Anchored patterns match whole-token identifiers; search patterns extract embedded
data from phrases (e.g. "IN PROGRESS (3)"->count_badge 3, the email out of a
sentence). Kept distinct from Semantic (VLM labels). Measured: task-detail gets
task_id/overdue_deadline/completion_ratio; board-dark gets the column count
badges (4/3/2) + a ratio — which combine with Milestone B groups to give
"column with N cards". Rule-based, no model. Unit-tested (matches + no false
positives). Grown iteratively from here.

Original plan:
Priority: MEDIUM-HIGH. Effort: medium (pattern library, iterative).
Attach a `semantic_hint` field to components whose OCR text matches known UI
patterns: `(Overdue)` in a red pill → `{hint: "overdue_deadline"}`; `2/5` next
to a progress bar → `{hint: "completion_ratio", value: "2/5"}`; `you@example.com`
→ `{hint: "placeholder_email"}`; `feat/PH-123-implement-login-screen` →
`{hint: "git_branch_ref"}`. The agent immediately knows what these components
MEAN without parsing raw text. Same philosophy as the icon-labeler (evidence-
attached interpretation) applied to text patterns. Start with 5–10 high-value
patterns; grow iteratively.

**Milestone E — Section-header association** (structural ~7→8) — DONE (2026-09-03)
`graph.AssociateHeaders` names each x-axis (column) repeated group with the text
token sitting directly above its top edge with x-overlap (ranked by smallest
gap, then overlap). y-axis (row) groups are skipped (a top-header there is
redundant/noise). Measured board-dark: the 3 kanban columns are named
"TO DO (4)", "IN PROGRESS (3)", "IN REVIEW (2)" (RepeatedGroup.header). Combined
with A/A2/B/D the agent now reads: kanban board -> named column (N cards) ->
card -> {label, assignee, checklist}. Deterministic; leaves unnamed when no
clear header (any "@ " prefixes are OCR noise, not this code). Unit-tested.

Original plan:
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

**Milestone F — Corner-style detection (rounded vs square)** (visual-verification) — DONE (2026-09-04)
Priority: MEDIUM (high value for visual-diff disputes). Effort: small (pixel test).
Motivation: an agent may be asked "you said list item 3 has rounded corners but I
see square — verify with refraict." Today refraict measures a region's bbox/color
but has NO border-shape signal, so it cannot settle that. It should be able to,
deterministically, from pixels it already has.

Method (validated as POC 2026-09-04): for a detected card/region/panel bbox,
sample the pixel AT each corner and compare it to (a) the region's interior fill
color and (b) the page background just outside the region. A ROUNDED corner shows
the background (fill clipped away); a SQUARE corner shows the fill. Vote across
the 4 corners (>=3 => rounded).

POC result — all three cases classified correctly:
- REAL Voirel card (board-dark r0005, known rounded): rounded 4/4 (corner px ==
  measured bg, d_interior=26, d_bg=0).
- synthetic square control: square 0/4 (corner px == fill, d_bg=80).
- synthetic rounded control: rounded 4/4.
So the pixel data refraict already has is sufficient. Emit `corner_style:
rounded|square` + confidence per card/region/panel component (and surface in the
MCP analyze summary). Caveats to handle in the real impl: anti-aliased corners
(sample a few px in, tolerance), and a LOW-CONFIDENCE guard when interior≈bg (the
zero-fill-contrast light-theme case, where the test is unreliable); vote >=3/4 to
tolerate an occluded corner. 4 pixel reads/region, no model.

Implemented: `detect.AttachCornerStyles` attaches `ir.CornerStyle{style,confidence,rounded_corners}` to card/region/panel components (in page.json). Corner pixel vs interior-fill vs page-bg, vote >=3/4, anti-aliasing inset, and a low-contrast guard that WITHHOLDS when interior==bg. Verified on board-dark: all 9 cards -> rounded 4/4 conf 1.00. Unit-tested (square, rounded, low-contrast-withhold, non-container-skip).

**Milestone F2 — Approximate border-radius magnitude (FUTURE / POC-validated, not built)**
Priority: LOW-MEDIUM. Effort: small (~40 lines, extends CornerStyle). Extends F
from binary rounded/square to an approximate radius, for "how rounded" questions
and cross-card consistency checks.

Method (POC 2026-09-04): for a rounded region, walk each corner's 45° diagonal
inward to the bg->fill transition; the diagonal distance converts to a radius
(for a quarter-circle, `r = d_diag / (sqrt2 - 1)`), median over the 4 corners.
Report `radius_px`, `radius_frac` (of the region's min dimension), and a coarse
bucket (none / subtle <=6px / moderate / large / pill).

POC results — measurement is SOUND at adequate contrast (synthetic light cards,
known radius):
  known 8px -> ~6.8, 16 -> 13.7, 24 -> 23.9, 40 -> 37.6  (consistent ~1-2.5px
  UNDER-bias, correctable with a ~+2px calibration offset; per-corner agreement
  tight, ~+-3px).
Constraint (the real limit): the radius WALK needs a clean bg->fill transition,
so it requires adequate fill/bg contrast. The dark-theme Voirel cards (fill vs
bg only ~26 units apart) are too low-contrast — the walk withholds there even
though the BINARY corner test (F) still works (it only needs relative closeness,
not a crisp edge). So F2 must: (1) apply only when F already said "rounded";
(2) apply a calibration offset; (3) WITHHOLD radius (keep rounded/square) when
fill/bg contrast is below threshold — honest "rounded, radius unmeasurable at
this contrast". Accuracy is ~+-2-3px: good for relative comparison and buckets,
NOT a pixel-exact CSS border-radius (anti-aliasing + circular-corner assumption
forbid exactness). Not implemented; recorded so a future build starts from this
evidence.

**Milestone G — Padding (x/y) + inter-sibling gaps** (spatial layout) — DONE (2026-09-04)
Priority: MEDIUM-HIGH (cleanest spatial signal; reuses A + B, no pixels/models).
Answers layout-consistency questions ("is padding/spacing uniform across cards?
is item 3 misaligned?") from pure box arithmetic on data we already have
(containment edges from A, repeated groups from B).

Padding (container edge -> nearest child edge, per side):
`padding_left = min(child.X0) - container.X0`, etc. POC on board-dark (9 cards):
- padding-LEFT median 36px, spread 8px (34-42) — tight/reliable.
- padding-TOP median 44px, spread 8px (38-46) — tight/reliable.
- padding-RIGHT/BOTTOM noisy (spread 114/93px) — CORRECT: R/B "padding" is really
  leftover whitespace after the last child (a 2-child card has huge bottom
  slack). So LEFT/TOP are the trustworthy content insets; RIGHT/BOTTOM are
  content-dependent and must be labeled as such (or paired with a "content fills
  container" flag). Padding accuracy also inherits child-detection completeness
  (a missed edge child inflates padding).

Inter-sibling gaps (adjacent repeated-group members): `next.X0 - cur.X1` /
`next.Y0 - cur.Y1`. POC board-dark: vertical card gaps a consistent 9px across
every column; horizontal column gaps 44/41px consistent across rows — the
cleanest signal of all. Frame as "space between siblings", NOT one-sided CSS
margin (the measured gap is margin-right(A)+margin-left(B) combined; not
attributable to one side).

Plan: attach `padding {left,right,top,bottom, content_fills}` to containers with
children; attach a `gaps`/spacing summary (median + spread) to repeated groups;
surface a compact rollup in the MCP analyze output. This is the LAST planned
milestone in this arc.

Implemented: `graph.AttachPadding` (ir.Padding{left,right,top,bottom,content_fills}
on containers, from containment edges) + `graph.AttachGroupGaps`
(gap_median/gap_spread on repeated groups). Surfaced in the MCP analyze summary as
`paddings` and `group_spacing`. Verified board-dark: card padding L~36/T~44
(content_fills flags the sparse cards); card columns gap 9px spread 0; column
rows gap 44px spread 3. Unit-tested.

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

### 2026-09-03 — OpenCV made a hard dependency (pure-Go detector removed)

Architectural decision (owner): the pure-Go region detector's weakness
undermined the deterministic story — it found 0 cards on board-DARK (not even the
hard case) vs. 9 for OpenCV, and CLAHE/Canny (the light-theme fixes) were
OpenCV-only. A tool whose core structural signals only work behind a build tag is
confusing, and "portable but poor results" is not a real selling point. So:
- Dropped the `opencv` build tag everywhere; the OpenCV detector compiles and is
  used unconditionally. `go build ./cmd/refraict` now requires system OpenCV 4.x.
- Removed the pure-Go detector (DetectRegions/RegionComponents + connected-
  components/union-find/mask helpers) and the `bild` dependency (go mod tidy).
  Kept the shared region TYPES + typing helpers (RegionBox, classifyRegion,
  regionSignals, ...) used by the OpenCV detector.
- Deleted the tagged CLI/dev wiring (regions_purego.go, dev regcheck purego).
- Rewrote README (Installation now leads with per-platform OpenCV install:
  apt/dnf/brew/pacman), Design principles ("Cross-platform, requires OpenCV 4.x"
  replacing "single static binary / no runtime deps"), removed the cross-compile
  claim, and the FAQ/config notes. Updated THIRD_PARTY_LICENSES (gocv REQUIRED,
  bild removed).
Tradeoff explicitly accepted: detection QUALITY over static-binary portability.
OpenCV is now a documented hard prerequisite. Next: the in-process Go MCP server
(`cmd/refraict-mcp`) on this unified single-path foundation.

### 2026-09-03 — MCP server (cmd/refraict-mcp)

Added an in-process Go MCP server exposing the pipeline to AI agents over stdio
(github.com/modelcontextprotocol/go-sdk v1.7.0, pinned). Tools: `analyze`
(bounded summary — page_type+confidence, component counts by type, repeated-group
count, grounding/crosscheck/consolidation_check scores — plus output_dir +
artifact paths), `inspect` (deterministic facts, no models), `get_artifact`
(read back a named artifact on demand, path-escape guarded). Reuses the pipeline
via a new exported `cli.Analyze(ctx, req)` entry point; `runAnalyze` was decoupled
from cobra (takes ctx) and a `quiet` flag suppresses stdout so it never corrupts
the MCP stdio JSON-RPC stream. Bounded-summary-plus-pointers design: decision
signals in the response, heavy data stays on disk. Smoke-tested end-to-end
(initialize/tools list/analyze/get_artifact). Builds on the opencv-only
foundation.

### 2026-09-06 — OCR PSM 6 vs 11 A/B (font-size tiering POC) — KEEP psm 6

While POC-ing font-size tiers (heading/body/caption from OCR token heights), the
`--psm 11` (sparse) mode produced cleaner multi-column LINE grouping than the
default `--psm 6` (single uniform block), which merges columns into one line.
Tempting to flip the wrapper default — but PSM is a SHARED default affecting the
whole pipeline (crop planning, grounding, hints), so A/B'd it across all 25
before any change (fresh caches, keep_alive warm):

  aggregate: OCR tokens 51.2 -> 50.2 (-1), crosscheck 0.94 -> 0.93 (flat),
             text_support 0.98 -> 0.98 (flat).
  per-image crosscheck: psm11 better=4 / WORSE=9 / equal=12.

So psm11 net-REGRESSES the downstream grounding metric (worse on more images —
e.g. board-dark cc 1.00->0.96, invite-dark 0.70->0.57, settings-light 1.00->0.95)
while only marginally helping line grouping. Decision: KEEP `--psm 6` as the
default. Verifying first avoided a blind-flip regression.

Implication for font-size tiering (if built): do it as a POST-OCR analysis inside
refraict on the token bboxes we already have (Tesseract's native block/line/word
structure gives correct reading order at psm 6 too; tier CLUSTERING works on
per-word heights regardless of line grouping) — NOT by changing OCR mode. POC
(sharpened): heading/body/caption tiers separate cleanly on single-column pages
(task-detail: "Implement login screen" 54px heading vs "Build the login form…"
22px body; login-light: title 34 vs labels 18), via k-means on filtered
(alnum>=2, sane-aspect) token heights. Recorded; not implemented.

Design locked (owner, 2026-09-06): PSM PER USE-CASE, not one global default. The
OCR wrapper already hard-codes best defaults that are env-overridable
(`REFRAICT_OCR_PSM`, default "6", now with an evidence comment in the wrapper).
So: the PIPELINE OCR keeps the verified psm 6; font-size/hierarchy tiering (when
built) runs its OWN scoped OCR pass with `REFRAICT_OCR_PSM=11` (column-clean line
grouping) used only to derive the tier signal — NOT feeding the token corpus, and
LAZY (only when the hierarchy signal is requested), so the second OCR pass is not
paid on every analyze. No global default change; no new mechanism (reuses the
existing env override).

**Milestone H — Typography hierarchy (font-size tiering) — DONE (2026-09-07)**
Implemented per the locked design as a POST-OCR analysis on the token/component
heights refraict already has (NOT a second OCR pass — the POC confirmed tier
CLUSTERING is independent of OCR line grouping, so the psm-11 rescan was
unnecessary for tiering itself and would only have helped column line-grouping,
which tiering does not need). `detect.AttachTextTiers` clusters the text
components' measured heights into up to three size bands via a deterministic
1-D k-means (quantile-seeded, no randomness), ranks the bands largest-first, and
labels each text component with `ir.TextTier{tier: heading|body|caption, level,
height_px, confidence}` (in page.json). A band-separation guard
(`MinBandSeparation`, default 0.18 of the larger center) collapses to all-`body`
on uniform-size pages so no spurious heading is invented; `confidence` is a
silhouette-style margin (how much closer the height is to its own band center vs
the nearest other band). It is a SIZE proxy only — explicitly NOT font
weight/family, which Tesseract cannot observe. Gated by
`analysis.detect_text_tiers` (default on); pure geometry, no model, no extra OCR.
The MCP `analyze` summary surfaces a BOUNDED per-band rollup (`text_tiers`:
tier/level/count/min-max height) rather than every component. Verified e2e on
deep-seek-ui.png: three clean non-overlapping bands (heading 24–60px ×10, body
13–22px ×19, caption 9–12px ×11). Unit-tested (3-band, 2-band, uniform-collapse,
non-text-skip, no-overwrite, empty, band confidence). Validated on the
hermes-stress 25-image set (login-light: heading 22px ×1 / body 12–15 / caption
8–9; settings-dark, board-dark, profile-light: clean 3-tier splits with headings
24–43px well above body 14–22px). Dense pages (board-dark, voirel-task-detail,
profile-light) initially showed the caption band's min height dipping to 2–3px —
OCR mis-boxes (e.g. a 3px "see"), not real captions. FIXED in the same milestone:
`AttachTextTiers` drops degenerate tokens whose height is below `MinHeightFrac`
(default 0.35) of the page's MEDIAN text height BEFORE clustering, so they are
left untiered (they remain text components for grounding/recovery — the shared
psm-6 OCR corpus is untouched) and no longer pollute the caption floor. A guard
falls back to tiering all when filtering would leave <2 candidates (uniformly-tiny
UIs). Post-fix caption floors: board-dark 7px, voirel-task-detail 8px,
profile-light 9px; clean-page login-light unchanged.

**Milestone I — Bold / font-weight detection — DONE (2026-09-08)**

Implemented: `detect.AttachTextWeights` (internal/detect/weights.go) attaches
`ir.TextWeight{weight: regular|heavy, stroke_px, baseline_px, confidence}` to
text components in page.json, using the validated recipe below. Gated by
`analysis.detect_text_weight` (default on); runs after AttachTextTiers on the
reconciled components. The MCP `analyze` summary surfaces `text_weights`: it
names EVERY heavy word (with confidence) plus a regular count — both refraict
(page.json, per-component) and MCP point to WHICH words are bold, not just a
count. Withholds (no attachment) in the uncertain band. Unit-tested (wordy gate,
confidence, synthetic thick-vs-thin separation, icon/junk exclusion).

Goal: label each text component's font WEIGHT (regular / heavy, with an
uncertain band; semibold as a lower-confidence stretch tier) so a consuming
agent can tell a bold heading/button/label from body copy — deterministically,
local-first, no model. This was investigated exhaustively across a POC series;
the sections below record the full arc so the decision (and the many rejected
approaches) is not re-litigated.

Design principle check: font weight is NOT reliably measurable the way height
(tiering) or corner pixels are — it is a low-dynamic-range signal at screenshot
resolution — so the bar was "does a deterministic method separate weight on REAL
UIs with acceptable, gated error." Only the final architecture below cleared it.

PROVEN ARCHITECTURE (multi-step, all deterministic; validated on the hermes 25):

  1. Candidate selection — run on RECONCILED components; keep only `type=="text"`
     with text. This REUSES existing infrastructure: the OpenCV region detector
     already types icon/logo/chart/image, and `dub.Reconcile` merges OCR tokens
     onto graphic regions — so those are excluded for free (same pattern as
     Milestone H's tier skip). No new icon detection.

  2. Per-token stroke width via distance-transform SWT (gocv, `-tags opencv`):
       a. crop the token bbox and upscale (~6x) — screenshot glyphs are ~1-3px
          stroke; measuring needs more pixels (same reason the OCR path upscales).
       b. AA-AWARE grayscale UNSHARP before Otsu (amount ~3.0) — THE KEY STEP.
          Regular strokes at low res are mostly anti-aliased soft edge; sharpening
          thins them MORE than a bold stroke's solid core, widening the bold/
          regular gap (measured: bold/regular stroke ratio 1.43 -> 1.76 as sharpen
          0 -> 3). Without this the classes overlap and bold headings are missed.
       c. Otsu binarize (auto-polarity for dark themes), distance-transform
          (DistL2), stroke width = 2 * mean(top-20% ridge DT values). SWT is robust
          to caps/tracking/glyph-mix where run-length ÷ height (`swPerH`) is not.

  3. Self-calibrating classification — NO synthetic reference font (those fail on
     calibration: a fixed font pool's bold is systematically heavier/lighter than
     the UI's actual bold). Instead compute the PAGE'S OWN regular-body baseline =
     median SWT stroke over body-tier tokens (height <= 1.3*median height; body
     text is overwhelmingly regular). Classify: HEAVY if stroke >= baseline *
     1.45; `uncertain` in a band just below (withhold — grounding-guard pattern);
     else regular. Self-calibration was stable across all 25 pages (baseline ~9-11
     stroke, light & dark themes).

  4. Three-layer junk filter (icons that OCR hallucinated into text-like strings
     still slip step 1 when they are small INLINE glyphs the region detector does
     not type as graphic — e.g. an avatar OCR'd as "'@'", a gear as "£63", a
     folder as "(C3)"):
       (a) region-type exclusion [step 1] — card/panel-sized icons/logos/charts.
       (b) letter-ratio "wordy" gate — require >=2 letters AND >=55% of non-space
           chars are letters. Kills symbol soup ("@", "£63", "@®"). Validated: it
           removed every pure-symbol FP on the stress set.
       (c) stroke-sanity ceiling — reject stroke > 3x the body baseline as non-text
           (physically impossible for real text; it is icon contamination). Killed
           the glyph+text merged FPs ("ene: VOIREL"@45px, "| Sprint Board"@31px).

VALIDATION (hermes 25 downsampled; per-page + a 14-word hand-labeled accuracy
check verified against pixels):
- Genuine headings/titles/buttons and thin body/labels classified correctly,
  INCLUDING the hard cases every earlier approach missed (Settings, VÖIREL,
  Profile, Implement, "Sign in to your account" H1, kanban column headers).
- 14-word labeled sample: 10 correct, 2 correctly WITHHELD as uncertain, 2 clear
  misses. Errors after the 3-layer filter concentrate in: 2-letter icon labels
  that are all-letters ("OF" — a grid glyph; passes the wordy gate, stroke only
  ~1.6x baseline so the ceiling can't catch it), thin ALL-CAPS ("List" — caps
  read slightly heavy), and one low-contrast red bold button ("Deactivate")
  under-called into the uncertain band. All firmly "a cloud VLM would also fumble
  these" cases.

REMAINING IMPLEMENTATION REFINEMENTS (before/at build): caps-aware normalization
(all-caps measures ~10% heavier — normalize to fix "List"); resolution-dependent
confidence (margin shrinks at small sizes / low DPI — reliability scales with
input resolution); semibold as a third, LOW-CONFIDENCE tier only (the
semibold↔bold boundary is ~5px at these sizes — regular-vs-heavy is the robust
cut). Attach `ir.TextWeight{weight: regular|heavy, stroke_px, confidence}` to
text components (gated; withhold in the uncertain band), behind an
`analysis.detect_text_weight` toggle, sitting after AttachTextTiers in the
pipeline (both consume reconciled text components).

APPROACHES INVESTIGATED AND REJECTED (with the killer evidence for each — do not
retry without new inputs/model):
- OCR-native `is_bold` (Tesseract `WordFontAttributes`) — DEAD. Only the LEGACY
  engine populated it; our LSTM build (`--oem` default) does not, and gosseract
  v2.4.1 exposes no font-attribute API. Would need a fork + legacy model.
- Isolated-crop stroke width — weak signal (MAD/median ~0.13); top outliers were
  OCR noise, not bold.
- VLM (gemma3:4b) weight perception — has essentially NO discriminative ability
  for weight and a fixed positional/answer bias. Verified on clean SYNTHETIC
  bold-vs-regular pairs: "regular" for both, thickness "3/10" for both; the
  collage/comparative framing only exposed the bias more. A vision model is the
  wrong tool for this perceptual discrimination at this scale.
- Collage relative stroke (compare tokens to each other) — confounded by glyph
  shape (two same-weight words differ ~1.5x in stroke).
- Synthetic same-word template matching (render "Settings" bold/regular, compare)
  — worked on medium/large clean text, FAILED on small/caps and on CALIBRATION
  (reference font weight != UI font weight); multi-font averaging did not fix the
  directional offset.
- Relative `swPerH` (stroke ÷ height) vs body baseline — FAILED: on real UIs
  weight and size covary, and at screenshot res sw/xh actually INVERTS (a bold
  heading is bigger, its stroke scales sub-proportionally, so its stroke-per-
  height is LOWER than body). Measured directly: Settings sw/xh 0.126 < Manage
  0.164.
- Preprocessing / vectorization to "recover" strokes — potrace vectorization
  NORMALIZES weight away (its stroke is set by the mkbitmap threshold, not the
  source) and made bold ≈ regular; confirmed the signal isn't a contrast problem.
  (Note: distinct from the earlier vtracer-for-VLM rejection — this was for pixel
  measurement, and separately fails.) Learned super-resolution rejected upfront:
  it would HALLUCINATE stroke thickness, corrupting the measured quantity, and
  needs a model.
- English-dictionary junk filter — REJECTED. Orthogonal to the actual problem:
  it KEEPS "OF"/"£63" (real word / plausible currency, but here they are icons)
  and would WRONGLY DROP proper-noun headings (VÖIREL, Hermes) that dictionaries
  lack — exactly the bold text we most want. "Is-English-word" != "is-text-vs-icon".
- Text-model plausibility gate ("does '£63' seem like text given the page title?")
  — REJECTED. Cannot undo an icon→string hallucination from TEXT alone: the
  hallucinated string looks fine ("£63" is a plausible price); only the PIXELS
  reveal it is a gear icon, which refraict already measures deterministically.
  Would add paid/probabilistic calls to replace a free, exact signal — against
  the local-first, measure-what-is-measurable principle.

Audit trail: five throwaway POC harnesses under `dev/` — `strokepoc` (run-length +
VLM vote), `collagemeasure` (common-frame), `swtpoc` (distance-transform SWT +
AA-sharpen), `swtregpoc` (self-calibrating stroke~height regression), `boldval` /
`boldval2` (consolidated + icon-exclusion + hybrid filter). Kept as the audit
trail, like the icon-label reliability PoCs.

**Milestone J — Layout hierarchy + occupancy shares (POC-VALIDATED; ready to build) — 2026-09-08**

Goal: give the agent the LAYOUT STRUCTURE of a UI — which regions nest inside
which, and how much of a container each child occupies along an axis — so it can
answer "list A has a header (~10%) and a body (~90%)" style questions and
reconstruct the DOM far more accurately. Answered as a MEASURED occupancy
fraction, explicitly NOT a claimed CSS property (flex:1 vs 10% vs a fixed px that
happens to be 10% are pixel-identical; refraict reports the observed share and
says "replicate however you like"). Deterministic, no model.

Two producers (a layout TREE) + one consumer (occupancy shares). All POC-
validated on the hermes-25 (harnesses under dev/, see Audit trail):

  PRODUCER 1 — Bordered nesting (containment tree).
    The OpenCV region detector already computes enclosure (`encloses[i]`) but
    `filterNested` deliberately DROPS any box >=90% contained in a larger one
    ("we want top-level regions... detecting genuine child elements is deferred
    to the medium-difficulty pass"). Un-suppressing that + relaxing MaxAreaFrac
    (0.60 filters page-spanning layout regions) and building a containment tree
    (parent = smallest strictly-containing box) yields real multi-level structure
    — depth 3-5 on content-rich pages. POC (dev/nestpoc): signup-dark surfaced a
    456x665 FORM container with 6 tiling vertical children (logo → heading →
    subtitle → name row → email → password → button → footer), verified visually.
    Bordered/carded layouts (forms, modals, auth cards, settings panels, task
    detail) — a large fraction of real UIs — become fully structured.

  PRODUCER 2 — Invisible containers (columns/rows with no border).
    Whitespace groupings (kanban columns, header/body splits) have no edges, so
    CV can't find them; they must be INFERRED from alignment. The naive approach
    (cluster raw boxes by shared x/y band) over-reached — a column absorbed the
    search bar sharing its x-band. FIX (dev/invcont2poc): SEED from RepeatedGroups
    (Milestone B — same-type, regularly-spaced sibling sets) instead of raw
    boxes, and gate by regularity (confidence = 1 - GapSpread/GapMedian; withhold
    < 0.5). This nails it: board-dark's TO DO (4) / IN PROGRESS (3) / IN REVIEW
    (2) columns are recovered EXACTLY (verified visually — no search-bar over-
    reach), each already NAMED via Milestone E header association, while icon/
    symbol noise clusters (GapSpread 283-1204) are withheld at conf 0.00. Reuses
    B (groups) + E (headers) + G (gap regularity) end to end; no new heavy CV.
    (Impl note: the RepeatedGroup Axis label reads inverted for this use — a
    vertical column of cards is Axis="x" because members share x — flip the
    naming in the real impl; geometry is correct.)

  CONSUMER — Occupancy shares (the flexbox/percentage question).
    For a container with direct children stacked on an axis: child_share =
    child_extent / children_span, plus a gap_fraction for unallocated space, and
    a tiling score (children coverage of the parent inner box) as confidence.
    Pure box arithmetic on the tree (dev/sharepoc). CRITICAL PREREQUISITE the POC
    proved: this is meaningless on the FLAT component set — 41/58 "containers"
    there had overlapping children (co-located card content, not tiling
    siblings), fractions summing >200%. It only works ON the layout tree above,
    where children genuinely partition an axis. So Producer 1/2 are hard
    prerequisites, not optional.

ARCHITECTURE — additive layer, `merged` stays canonical (HARD CONSTRAINT to
avoid regressions). Analysis of the blast radius: ~15 features consume the flat
`merged` set (colors, graph, padding, repeated-groups, corner-styles, crosscheck,
DOM, page-type, tiers, weight, page.json, MCP...). Injecting the nested tree INTO
`merged` would regress several: `graph.Build` is O(n²) and would emit a `contains`
edge per nesting level (exploding relationships + corrupting Milestone G padding
against grandchildren); page-type/crosscheck/pageConfidence counts would shift;
and filterNested's revival brings back the exact OpenCV artifacts (double-contour
twins, chart-gridline boxes) it was added to kill — page.json bloat. Therefore:
  - Keep the FLAT `merged` set canonical. Every per-element MEASUREMENT feature
    (colors, corner-style, text tier, text weight, crosscheck) stays flat,
    independent, unregressed — these are leaf-node MEASURED facts, orthogonal to
    nesting and higher-confidence than the inferred tree.
  - Emit the hierarchy as a SEPARATE additive artifact (`layout.json` /
    `Graph.LayoutTree`) that REFERENCES component IDs (like RepeatedGroup.
    MemberIDs / Relationship.A,B / Component.Children already do), never owns or
    mutates components, and carries its own confidence so consumers withhold when
    the grouping is uncertain — without that uncertainty leaking into the flat
    measurements.
  - Only STRUCTURAL features opt in: DOM inference (the biggest win — it is
    currently the weakest, guessiest output and is EXACTLY what a measured layout
    tree is for; rewiring `probableDOM` to consume the tree is the highest-value
    single use, above flex-share), padding (grandchild-contamination fix),
    occupancy shares, and summary framing.
  Same additive pattern every prior milestone (B/E/G, tiers, weight) used — none
  regressed others because none changed the reconciled set.

ACCEPTANCE GATE (regression guard): on the hermes-25, component count, Milestone
G padding numbers, and crosscheck scores must be UNCHANGED vs pre-milestone
(the layout tree is purely additive). Inferred containers must be confidence-
gated (regularity >= threshold) and withhold ambiguous clusters.

Known limits: invisible-container inference is bounded by RepeatedGroup detection
(needs >=2 regularly-spaced same-type siblings — a 2-child header/body split with
dissimilar children won't seed a group and stays unstructured unless bordered);
reliability of occupancy shares scales with detection completeness (a missed
child skews fractions — gate on the tiling score). The flex/percentage INTENT
(distinguishing flex:1 from 10% from 48px) is NOT deterministically recoverable
from one screenshot and stays out of scope (belongs in the inferred DOM, marked
inference).

Audit trail: dev/sharepoc (occupancy on flat set — showed the prerequisite),
dev/nestpoc (bordered containment tree), dev/invcontpoc (naive invisible — over-
reach), dev/invcont2poc (RepeatedGroup-seeded + gated — the validated approach).
The `filterNested` refactor (moved from the inner pass to the top-level
`DetectRegionsOpenCV`, behavior-preserving, tests green) and the POC-only
`DetectRegionsOpenCVRawPOC` export are in place for the build.

### 2026-09-06 — OCR adapter moved to scripts/refraict-ocr; Go-rewrite milestone

The OCR adapter was living in the gitignored `e2e-test/` dir (never tracked) even
though it's a functional pipeline dependency (README install step; the OCR path
shells to it). Moved it to `scripts/refraict-ocr` (tracked, proper home; dropped
the `.py` — it's an installable executable), updated README (refraict + qa) and
reinstalled from the new path.

**Milestone — In-process Tesseract via gosseract (CGo) — DONE (2026-09-06)**
Priority: MEDIUM. Ship OCR in-process the SAME way OpenCV is shipped (CGo), instead
of shelling out to the Python adapter. Use `github.com/otiai10/gosseract/v2`
(CGo bindings to libtesseract+leptonica). OCR becomes an in-process call; the
Python/Pillow/subprocess layer goes away entirely. Keep `REFRAICT_OCR_CMD` as an
OPTIONAL override (PaddleOCR/cloud), but in-process Tesseract is the default —
works out-of-the-box, no OCR install step for the user.

Feasibility spike (2026-09-06) — VIABLE:
- gosseract v2.4.1 compiles + runs against system tesseract 4.1.1 / leptonica
  1.82 (dev libs: libtesseract-dev + libleptonica-dev, version-matched, in apt).
- Token yield matches the Python adapter (login-light 27 vs 24; verify-email-dark
  24 vs 24). The preprocessing is the value-add, NOT the OCR call — and it ports
  cleanly: `internal/imageproc` already has invert + resize.
- Impl: imageproc invert(dark-theme) + 2x upscale -> gosseract
  GetBoundingBoxes(RIL_WORD) -> DIVIDE boxes back to original coords (the upscale
  factor, as the Python adapter does) -> emit the same token structure.
- Net user deps DECREASE: today needs tesseract binary + Python + Pillow; this
  needs only tesseract/leptonica dev libs (the tesseract runtime was already
  required). README prereqs gain libtesseract-dev/libleptonica-dev next to OpenCV.

Verification gate before making it the DEFAULT: the same 25-image A/B (token
counts + text_support) — Go+leptonica resampling isn't byte-identical to Pillow,
so confirm no OCR-quality regression. DONE + VERIFIED: `internal/ocr.TesseractEngine`
(imageproc invert(dark) + 2x upscale -> gosseract RIL_WORD boxes -> coord-divide),
default when REFRAICT_OCR_CMD is unset; external command still overrides. A/B
across the 25 (in-process GO vs Python adapter PY): OCR tokens GO 51.1 vs PY 51.2
(25/25 within +-15%, most exact), text_support GO 0.972 vs PY 0.968 (+0.004),
crosscheck GO 0.935 vs PY 0.951 (-0.015, two invite-page outliers). Quality-
equivalent -> made default. README prereqs updated (libtesseract-dev +
libleptonica-dev alongside OpenCV). Unit-tested (normalizeToken, invert/luminance,
upscale).

Follow-up (2026-09-06): the Python adapter (scripts/refraict-ocr) was REMOVED —
in-process Tesseract makes it redundant, and removing it keeps refraict pure
Go/CGo with no Python/Pillow anywhere in the repo. The REFRAICT_OCR_CMD external-
engine hook remains (documented in README) for plugging in PaddleOCR/cloud.

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
