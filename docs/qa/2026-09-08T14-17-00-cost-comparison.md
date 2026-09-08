# Refraict — Cost comparison: direct multimodal image read vs refraict-backed queries

**Date:** 2026-09-08T14:17:00-06:00
**Scope:** Real credit-cost measurement of an agent (Kiro CLI) answering questions
about a UI screenshot two ways: (a) a direct multimodal image read on each turn,
vs (b) the `refraict-ui` agent using the refraict MCP server (analyze once, then
selective query tools). Image: `journeys.png` (Asteria/Journeys, 390×1537 mobile).

---

## Result

| Path | Credits per turn |
| --- | --- |
| Direct multimodal image read (describe the screenshot) | **1.93** |
| refraict-backed iteration (query the prior analysis) | **≤ 0.5** |

~**4× cheaper per turn**, and the gap **compounds** over a conversation: the
normal usage pattern is "analyze once, ask many questions," and every follow-up
on the refraict path is a cheap text turn, whereas the direct path re-pays the
full image cost on every turn.

## Why the difference (mechanism, not just the number)

- **Direct read** re-ingests the full image tokens into the paid multimodal
  model on EVERY turn, and yields a *description* (model impression), not
  measurement.
- **refraict path** does the expensive vision/OCR work ONCE in `analyze`,
  LOCALLY on Ollama (not billed to the agent's model), writing structured facts
  to disk. Follow-up questions are cheap TEXT turns over a small filtered slice
  returned by the selective query tools (`get_components`/`query_text`/
  `get_container_children` — 0.5–5 KB payloads, not the 323 KB `page.json` and
  not a re-read of the image).

## Better AND cheaper

The refraict facts are **measured** (exact hex colors, pixel geometry,
stroke-based font weight, occupancy shares), **grounded/traceable** (the agent
cites the specific measurement behind each claim), and **reusable** (analyze
once, query many times, across turns/sessions). This is the cost-saving
cross-check pattern the tool was built to enable — now demonstrated with real
credit numbers.

## Division of labor (validated)

refraict presents FACTS; the agentic tool (Kiro) REASONS over them. In the same
session the agent correctly inferred that the "EXAMINE EPHEMERIS RECEIPT" button
was disabled — from refraict's MEASURED low text/background contrast (fg ≈ bg)
plus layout context — and labeled it as an inference from visual presentation.
refraict never claimed the interactive state (an unobservable fact it must not
assert); the agent reasoned to it from grounded measurements. Clean separation:
facts flow up cheaply, interpretation happens in the reasoning engine.

## Follow-ups (improve the cheap side of the ledger)

Two refraict-core refinements observed during these sessions would enrich the
FACTS without changing this cost profile: inline icon-glyphs OCR'd as heading-
tier "text" (e.g. "& ", "©@") should be typed/filtered as graphic, and page-type
classification returned "Generic (confidence 0)" on this rich UI (classifier did
not fire). Both are measurement refinements, not agent issues.
