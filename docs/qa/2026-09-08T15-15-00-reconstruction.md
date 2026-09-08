# Refraict — UI reconstruction from measured facts alone (no image access)

**Date:** 2026-09-08T15:15:00-06:00
**Scope:** A Kiro agent (the `refraict-ui` agent, fs_read BLOCKED) rebuilt an
HTML/CSS rendering of `journeys.png` (Asteria/Journeys) using ONLY refraict's
measured facts via the MCP tools — it never saw the image. Artifacts:
`/mnt/d/Downloads-D/asteria-project/msedge_*.png` (rendered reconstruction).

---

## Result

The reconstruction is remarkably faithful to the original, validating the whole
architecture end-to-end in the most demanding way: a full visual rebuild from
measured facts. It is faithful EXACTLY to the degree refraict measures each
thing, and it DEGRADES HONESTLY where refraict does not (empty placeholder for
the unmeasured route graphic; generic stand-ins for un-vectorized icons) rather
than fabricating.

### Faithful (measured facts flowing through)
- Full vertical LAYOUT/structure: header → ACTIVE JOURNEY → serif title → journey
  card → 3-column metrics row → Under This Sky → Celestial Signature card → Field
  Note → LOG ARRIVAL + EXAMINE EPHEMERIS RECEIPT buttons → 4-tab bottom nav.
  (Milestone J layout hierarchy + occupancy.)
- TEXT verbatim (OCR): title, km/bearing/coords, italic quotes, "Coronal Flux
  Root #C-193", "REF AST-2609-117".
- COLORS: dark theme, teal accents (JOURNEY ACTIVE, TRUE NORTH), bronze/amber
  (title metrics, nav). (Measured hex samples.)
- TYPOGRAPHY tiers: serif display heading vs small-caps labels vs body.
- FONT WEIGHT (Milestone I): "Point Reyes Meridian Ridge" rendered bold — the one
  heavy-weight token refraict measured.
- The DISABLED "EXAMINE EPHEMERIS RECEIPT" button rendered greyed/low-contrast —
  the agent's earlier inference (from measured fg≈bg contrast + layout context)
  carried into the rebuild.

## Three distinct gap sources (only one is a true refraict limit)

1. **Facts refraict does NOT measure** (true tool limit): the route
   visualization's internal shape (diagonal path, origin/destination markers,
   horizon arc) — a non-text graphic with no OCR and no detected sub-structure;
   the agent left an empty box (honest). Icon VECTOR SHAPES — typed/labeled
   (target/compass/eclipse) but not captured as geometry, so rendered as generic
   stand-ins.
2. **Facts refraict measures but the agent DID NOT fetch** (prompt/agent, not
   tool): this rebuild used a GENERAL agent that pulled facts opportunistically,
   not one instructed to EXHAUSTIVELY gather every measured detail. Exact
   paddings (`get_components(has=padding)`) and per-element colors were available
   but not all requested — a reconstruction-oriented agent with a "gather all
   measured facts first" directive would tighten spacing/color fidelity. The data
   was there; it just wasn't all queried.
3. **Fact PRECISION** (refraict refinement): some accent colors shifted (nav
   icons purple-ish vs bronze/teal) — the foreground-vs-background color-sampling
   nuance (a sample can capture the region/background rather than the exact glyph
   color).

## Prioritized follow-ups this surfaced
- **Icon fidelity**: vectorizing icon glyphs to reproduce shape in a
  reconstruction. Note: vectorization was REJECTED earlier for VLM-FEEDING (crisp
  vectors are off-distribution for the model), but reconstruction is a DIFFERENT
  use — reproducing measured shape, not feeding a model — so it may have a
  legitimate second life here.
- **fg-vs-glyph color precision**: sample the glyph ink color, not just the
  region/background, for text components.
- **Reconstruction-oriented agent prompt**: a variant that exhaustively pulls
  paddings + per-element colors + layout shares before rebuilding (closes gap #2
  with no tool change).

## Takeaway
Arguably a stronger validation than the credit-cost numbers: it shows refraict's
facts are complete and structured enough to REBUILD the UI, and it makes the
remaining gaps concrete and prioritizable. "Measure what's measurable, present
it, let the agent reason" — rendered literally as pixels.
