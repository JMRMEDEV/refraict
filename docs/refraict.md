# Refraict

## Implementation Guide for a Portable, Efficient UI/UX Screenshot Analysis Pipeline

> **Evolution note (living).** This document is the original design spec. The
> shipped implementation has since evolved in several places based on measured
> results; those changes are summarized in per-section quotes below and tracked
> in full in [`docs/roadmap/gaps-vs-vision-llm.md`](roadmap/gaps-vs-vision-llm.md).
> The biggest divergences from this spec:
> - **Components are deterministic, not VLM-emitted.** Small local VLMs cannot
>   reliably output bounding boxes, so components come from OCR + a CV region
>   detector; the VLM only writes grounded natural-language descriptions.
> - **Crop planning defaults to a bounded overview+grid**, not OCR-density
>   subdivision (which exploded the crop count and overwhelmed the model).
> - **A grounding guard** flags summary claims unsupported by measured evidence.
> - **Non-text elements** (icon/logo/chart) are detected (OpenCV Canny, opt-in
>   build tag) and labeled by **multi-run VLM voting** over a Lucide alias map.
> - **Memory controls**: models default to `keep_alive:0` (freed immediately),
>   with an opt-in `--keep-warm` for batch runs.

## 1. Purpose

**Refraict** is a CLI-first system for analyzing screenshots of web and application interfaces and converting them into reusable, structured, AI-friendly context.

The system should inspect UI screenshots and extract both deterministic visual facts and semantic interpretation, including:

- visible components
- labels and text
- inferred semantic roles
- probable DOM/component structure
- spatial layout and grouping
- approximate design tokens
- colors, preferably measured from pixels
- typography characteristics when inferable
- logos and brand marks when recognizable
- component relationships
- repeated UI patterns
- uncertainty and confidence
- concise natural-language descriptions suitable as context for other AI tools

Refraict should be designed around **small local models first**, using deterministic computer vision and OCR to reduce the amount of reasoning required from the model.

The key architectural principle is:

> Use deterministic tools for facts that can be measured directly, small vision models for local semantic interpretation, small text models for compression and synthesis, and larger models only when global reasoning or escalation is necessary.

---

# 2. Primary Goals

Refraict should optimize for:

1. **Low operating cost**
   - Default to local inference.
   - Avoid unnecessary cloud API calls.
   - Cache every reusable stage.

2. **Portability**
   - Work as a CLI on macOS, Linux, and Windows where practical.
   - Avoid unnecessary runtime dependencies.
   - Keep the main orchestrator as a simple executable.

3. **Efficient use of small models**
   - Use 3B–4B-class vision models for narrow local tasks.
   - Keep model weights warm between crop analyses.
   - Prefer one persistent model instance with queued or batched work over multiple duplicated instances.

4. **High-quality structured output**
   - Maintain a canonical intermediate representation rather than allowing every model to invent its own schema.
   - Record provenance and confidence for every important property.

5. **Useful AI context**
   - Generate both JSON and natural-language descriptions.
   - Produce region-level and page-level summaries suitable for another AI agent to consume directly.

6. **Graceful escalation**
   - Detect uncertainty.
   - Escalate only difficult regions or pages to a stronger model.

---

# 3. Non-Goals

Refraict should not claim to recover information that does not exist in pixels.

Examples of things that cannot be reliably reconstructed from screenshots alone:

- actual HTML source
- CSS class names
- hidden elements
- ARIA properties
- DOM event handlers
- JavaScript behavior
- hover-only states unless visible
- exact font family when visually ambiguous
- exact semantic HTML tag in all cases

Refraict may infer a **probable semantic DOM/component tree**, but this must remain clearly marked as inference.

---

# 4. Recommended Technology Stack

## Core language

**Go** is the recommended primary implementation language.

Why Go:

- excellent CLI ergonomics
- straightforward cross-platform compilation
- simple concurrency model
- low memory overhead
- excellent HTTP and JSON support
- good fit for orchestration and worker queues
- easier development and maintenance than a lower-level systems stack for this workload

Rust remains a strong alternative if future requirements include embedding inference runtimes directly, heavy SIMD image processing, custom memory management, or deep GPU/native integrations.

For the current architecture, Go is the better default.

## Suggested Go libraries

- CLI: `cobra`
- logging: standard `log/slog`
- HTTP: standard `net/http`
- JSON: standard `encoding/json`
- hashing: standard `crypto/sha256`
- image primitives: standard `image` + `golang.org/x/image`
- config: plain JSON/YAML/TOML or `viper` only if needed
- SQLite: `modernc.org/sqlite` if avoiding cgo is desirable

## Image processing

Prefer simple native Go operations where practical.

For large images, strongly consider **libvips** as an external dependency or sidecar because it is efficient for:

- huge screenshots
- resizing
- thumbnails
- crop extraction
- format conversion
- streaming/demand-driven image processing

Use OpenCV only where necessary for more advanced CV operations such as:

- connected components
- contours
- adaptive thresholding
- morphology
- region segmentation
- separator detection

Avoid making OpenCV a hard dependency until the simpler image stack proves insufficient.

## OCR

OCR should run independently of the vision model.

Possible implementations:

- PaddleOCR
- RapidOCR
- another local OCR engine with bounding-box output

Required OCR output:

- text
- bounding box
- confidence
- optionally text orientation

## Local model runtime

Support model backends through adapters.

Initial backend:

- Ollama

Recommended future-compatible backends:

- llama.cpp / llama-server
- OpenAI-compatible local servers
- cloud APIs

Do not couple the pipeline architecture to Ollama-specific behavior.

---

# 5. High-Level Architecture

```text
                         INPUT SCREENSHOT
                                │
                                ▼
                       deterministic ingest
                                │
                    ┌───────────┴───────────┐
                    │                       │
                    ▼                       ▼
                  OCR                  image/CV pass
                    │                       │
                    └───────────┬───────────┘
                                ▼
                         region proposal
                                │
                                ▼
                       multi-scale crop plan
                                │
                                ▼
                         3B–4B local VLM
                                │
                                ▼
                        raw crop analyses
                                │
                                ▼
                     normalize + deduplicate
                                │
                                ▼
                          canonical UI IR
                                │
                 ┌──────────────┴──────────────┐
                 │                             │
                 ▼                             ▼
          deterministic facts           3B text summarizer
                 │                             │
                 ▼                             ▼
       coordinates / colors             region summaries
                                               │
                                               ▼
                                        page summarizer
                                               │
                                               ▼
                                        page description
                                               │
                                               ▼
                                      optional aggregator
                                               │
                                               ▼
                                  inferred UI tree / DOM plan
                                               │
                                               ▼
                                       optional escalation
```

---

# 6. Processing Philosophy

The small vision model should not be asked to do tasks that deterministic software can perform more reliably.

## Deterministic responsibilities

Use non-LLM methods for:

- exact crop coordinates
- image dimensions
- resize ratios
- OCR bounding boxes
- pixel sampling
- measured colors
- basic edge detection
- region geometry
- overlap calculations
- duplicate detection support
- hashing
- cache validation

## Vision model responsibilities

Use the local VLM primarily for:

- semantic component classification
- grouping
- visual hierarchy
- probable component roles
- identifying cards, buttons, inputs, navigation, tabs, etc.
- interpreting charts or uncommon visual patterns
- understanding relationships that are difficult to derive from geometry alone
- local design interpretation
- logo/brand guesses with confidence

## Text-model responsibilities

Use a text-only model for:

- condensing raw crop observations
- merging nearby observations
- producing semantic descriptions
- identifying repeated patterns
- generating page-level summaries
- global reasoning over structured observations

---

# 7. Image Ingest and Resolution Strategy

Refraict must support ultra-high-resolution screenshots without feeding the entire original image directly to a small VLM.

## Keep the original immutable

Never destroy the original screenshot.

Maintain:

- original image
- page overview image
- region crops
- optional detail crops

## Resolution pyramid

Recommended starting values:

### Page overview

- target width: approximately `800–1200 px`
- purpose: global page structure only

### Normal analysis crops

- target longest side: approximately `1024–1536 px`
- default starting point: `1280 px`

### Dense regions

- target longest side: approximately `1280–1800 px`

### Tight detail crops

- approximately `768–1200 px`

## Text-size preservation rule

Resize decisions should be based partly on expected text height after scaling.

Recommended initial rule:

> If median detected text height would fall below approximately 12 px after resizing, subdivide the crop instead of shrinking it further.

Example:

```text
crop height: 3000 px
median OCR text height: 28 px
resize target: 1000 px

scale = 1000 / 3000 = 0.333
28 × 0.333 ≈ 9.3 px

Result: too small → subdivide.
```

## Resize quality

Use a high-quality downsampling filter such as Lanczos.

---

# 8. Crop Planning

> **Evolved to:** the default crop planner is a **bounded overview + fixed
> Rows×Cols grid** (`crop_strategy: "grid"`), producing exactly `1 + rows*cols`
> VLM calls. The OCR-density-driven adaptive subdivision described below is
> retained as an opt-in (`crop_strategy: "adaptive"`) but is *not* the default:
> on text-dense pages it exploded the crop count (e.g. 55 crops) and, combined
> with a large model, exhausted memory. The grid keeps calls bounded and a
> single model warm within a run.

Naive quadrants should not be the default.

## Region-aware crop generation

Use OCR and image/CV signals to propose semantically useful regions.

Possible region signals:

- whitespace boundaries
- strong horizontal separators
- strong vertical separators
- card-like rectangular regions
- text-density clusters
- large background changes
- repeated layout structures
- connected components

## Overlap

Neighboring crops should overlap.

Recommended starting point:

- `15–25% overlap`
- default: `20%`

This prevents components from being cut precisely at crop boundaries.

## Multi-scale analysis

Use at least three conceptual levels:

### Level 0 — overview

Purpose:

- page type
- overall structure
- large sections

### Level 1 — section/region

Examples:

- header
- sidebar
- hero
- content grid
- chart area
- table
- footer

### Level 2 — local detail

Examples:

- button groups
- individual cards
- controls
- form fields
- labels
- icons

---

# 9. Worker Inference Strategy

## Keep one model warm

Do not run multiple identical model copies unless there is a demonstrated throughput reason.

Recommended architecture:

```text
persistent 3B–4B VLM process
        │
        ▼
analysis queue
        │
        ▼
small adaptive batches
```

The model weights remain resident in memory between requests.

## Sequential vs batched

On one GPU:

- strict sequential execution is acceptable
- small batches may improve GPU utilization
- uncontrolled concurrent inference should be avoided

Suggested initial settings:

- inference workers: `1`
- batch size: `1–4`
- increase only after benchmarking

## Prompt-prefix reuse

Keep the analysis instructions and schema stable across requests so compatible runtimes can potentially benefit from prompt/prefix caching.

---

# 10. Crop-Level Vision Output

> **Evolved to:** components are **not** produced from VLM-emitted bounding
> boxes — small local VLMs cannot do that reliably (they returned malformed or
> empty JSON). Instead, components are synthesized deterministically from OCR
> tokens and a CV region detector, and the VLM is asked only for a *grounded*
> natural-language description constrained to the measured OCR text and colors.
> A deterministic grounding guard then flags any summary claim (color, number,
> quoted text, non-observable behavior) unsupported by the evidence.

Every crop analysis should return both:

1. structured JSON
2. a concise semantic description

## Example structured output

```json
{
  "crop_id": "c17",
  "bbox_global": [1200, 800, 2400, 1800],
  "role_guess": "kpi_section",
  "layout": {
    "type": "grid",
    "columns": 3,
    "gap_px_approx": 24
  },
  "components": [
    {
      "id": "c17-node-1",
      "type": "card",
      "bbox_global": [1240, 840, 1580, 1120],
      "confidence": 0.92
    }
  ],
  "confidence": 0.90
}
```

## Example semantic description

```text
This region is a KPI summary section containing three equally sized cards
arranged in one horizontal row. Each card contains a muted label, a large
metric value, and a smaller trend indicator. The cards appear visually
consistent and are likely reusable instances of the same component.
```

The semantic description should add interpretation rather than merely restating JSON fields.

---

# 11. OCR Integration

> **Evolved to:** OCR now **auto-inverts dark-theme UIs** (light-on-dark text
> defeats Tesseract, which expects dark-on-light) and upscales small text before
> recognition — this turned garbled output into accurate labels on dark
> dashboards. OCR is also promoted from a hint to a primary source: OCR tokens
> become text components directly. A residual-error path (swapping Tesseract for
> PaddleOCR/RapidOCR) is noted in the roadmap but not yet adopted.

OCR should happen before or alongside VLM analysis.

The vision worker may receive OCR context for its crop.

Example:

```json
[
  {
    "text": "Revenue",
    "bbox_global": [1310, 895, 1390, 918],
    "confidence": 0.99
  },
  {
    "text": "$42,891",
    "bbox_global": [1310, 935, 1420, 975],
    "confidence": 0.98
  }
]
```

The VLM then answers questions such as:

- which text belongs to which component?
- is this a metric card?
- is this label secondary or primary?
- are these repeated instances of the same component?

This prevents the small VLM from spending capacity on OCR when a dedicated OCR engine can do it more reliably.

---

# 12. Color Extraction

Models should not be trusted for exact color values when pixel sampling is possible.

Recommended approach:

1. model identifies a component and approximate region
2. deterministic code samples interior pixels
3. remove obvious border/text contamination where possible
4. compute representative RGB/HEX

Example result:

```json
{
  "background": {
    "value": "#2563EB",
    "source": "pixel_sampler",
    "confidence": 0.997
  }
}
```

Use the model only for semantic interpretation of the color, such as:

- primary accent
- danger state
- muted surface
- active navigation item

---

# 13. Coordinate System

All model observations must ultimately resolve to a canonical global coordinate system.

Store both local and global coordinates if helpful.

Example:

```json
{
  "bbox_local": [100, 150, 280, 198],
  "bbox_global": [700, 1150, 880, 1198]
}
```

Never make the final aggregator guess global geometry from prose alone.

---

# 14. Canonical UI Intermediate Representation

Refraict should define its own canonical **UI IR**.

Models are adapters into this representation, not authorities over the schema.

Example:

```json
{
  "id": "node_132",
  "type": {
    "value": "button",
    "source": "qwen_vl",
    "confidence": 0.89
  },
  "bbox": {
    "x": 820,
    "y": 410,
    "width": 148,
    "height": 44,
    "source": "geometry"
  },
  "text": {
    "value": "Continue",
    "source": "ocr",
    "confidence": 0.992
  },
  "appearance": {
    "background": {
      "value": "#2563EB",
      "source": "pixel_sampler",
      "confidence": 0.997
    },
    "foreground": {
      "value": "#FFFFFF",
      "source": "pixel_sampler",
      "confidence": 0.99
    },
    "radius_px": {
      "value": 8,
      "source": "cv",
      "confidence": 0.82
    }
  },
  "semantic": {
    "role": "primary_action",
    "source": "qwen_vl",
    "confidence": 0.87
  },
  "children": []
}
```

---

# 15. Provenance Rules

Every significant value should be able to record:

- value
- source
- confidence
- optional source-run ID

Preferred source priority for conflicting values:

```text
measured geometry > VLM geometry guess
OCR text > VLM transcription
pixel color > VLM color guess
VLM semantics > CV semantics
aggregated consensus > isolated low-confidence inference
```

Do not silently overwrite conflicting evidence.

---

# 16. Duplicate Reconciliation

Overlapping crops will produce duplicate observations.

Refraict must reconcile them.

Useful signals:

- bounding-box IoU
- normalized text equality
- component type similarity
- spatial proximity
- shared OCR content
- visual similarity if available

Starting heuristic:

```text
IoU > 0.65
AND
same/similar component type
AND/OR
same OCR text
→ likely duplicate
```

When evidence conflicts, preserve the disagreement and compute a merged confidence rather than arbitrarily choosing one result.

---

# 17. UI Graph Before DOM

Do not generate DOM immediately.

First create a semantic UI graph.

Example:

```text
Page
├── Header
│   ├── Logo
│   ├── Navigation
│   └── CTA
├── Sidebar
└── Main
    ├── PageTitle
    ├── MetricsGrid
    │   ├── MetricCard
    │   ├── MetricCard
    │   └── MetricCard
    ├── Chart
    └── DataTable
```

Also store relationships explicitly:

```json
{
  "relationships": [
    ["card-1", "same_row", "card-2"],
    ["card-2", "same_row", "card-3"],
    ["sidebar", "left_of", "main"],
    ["metric-label", "inside", "card-1"],
    ["metrics-grid", "below", "page-title"]
  ]
}
```

Only after the graph is stable should Refraict infer probable DOM or component code.

---

# 18. Summarization Pipeline

Refraict should maintain multiple levels of textual context.

## L0 — raw crop description

Generated directly from each vision run.

Purpose:

- preserve local interpretation
- debugging
- retrieval

## L1 — region summary

Generated from nearby crop observations after normalization and deduplication.

Example:

```text
REGION ROLE:
KPI summary section of an analytics dashboard.

STRUCTURE:
Three equal-width cards in one horizontal row.

CONTENT:
Each card contains a muted label, large metric value, trend indicator,
and supporting text.

VISUAL STYLE:
White card surfaces on a pale gray background, subtle borders,
approximately 8px corner radius, and consistent spacing.

SPATIAL RELATIONSHIPS:
The cards share aligned top and bottom edges and consistent widths.

SEMANTIC INTERPRETATION:
These are likely reusable KPI card components.

UNCERTAINTIES:
Exact font family and interaction behavior cannot be determined.
```

## L2 — page summary

Generated from region summaries and the low-resolution page overview.

Example:

```text
The screenshot shows a desktop analytics dashboard using a persistent
left sidebar and a light main content surface. The main area begins with
a title and utility controls, followed by a three-column row of KPI cards,
a wide data visualization, and a tabular section. The visual system uses
subtle borders, medium rounded corners, restrained blue accents, and a
spacing system that appears based on regular small increments.
```

## Why summaries are first-class artifacts

They make Refraict output usable as context for:

- coding agents
- web reconstruction agents
- design agents
- QA systems
- RAG systems
- comparison tools
- model-to-model workflows

The summary should explain what the interface **means**, not merely repeat serialized fields.

---

# 19. Storage Layout

A recommended project layout:

```text
analysis/
├── manifest.json
├── page.json
├── page.md
├── graph.json
├── overview.png
│
├── regions/
│   ├── header.json
│   ├── header.md
│   ├── metrics.json
│   ├── metrics.md
│   └── ...
│
├── crops/
│   ├── c001.json
│   ├── c001.md
│   ├── c002.json
│   ├── c002.md
│   └── ...
│
└── evidence/
    ├── ocr.json
    ├── colors.json
    └── regions.json
```

SQLite may additionally store indexed metadata, cache references, model runs, and provenance.

---

# 20. Cache Design

Every deterministic or expensive stage should be cacheable.

Recommended cache-key pattern:

```text
input hash
+
stage version
+
model version
+
prompt version
+
schema version
=
cache key
```

Examples:

```text
image SHA256 + crop algorithm version
→ crop plan cache

crop SHA256 + OCR model version
→ OCR cache

crop SHA256 + VLM model + prompt version
→ vision cache

normalized region hash + summary prompt version
→ region-summary cache
```

Changing the final aggregator should not force a rerun of vision inference.

---

# 21. Prompt Versioning

Treat prompts as versioned software artifacts.

Example IDs:

```text
crop-analysis-v1
crop-analysis-v2
region-summary-v1
page-summary-v1
ui-graph-v3
```

Every run should persist:

```json
{
  "model": "qwen-vl-3b",
  "prompt_version": "crop-analysis-v4",
  "schema_version": "ui-ir-v2"
}
```

This makes evaluation reproducible.

---

# 22. Model Adapters

Define generic model interfaces.

Conceptual Go interface:

```go
type VisionBackend interface {
    Analyze(ctx context.Context, req VisionRequest) (*VisionResult, error)
}

type TextBackend interface {
    Complete(ctx context.Context, req TextRequest) (*TextResult, error)
}
```

Possible implementations:

- OllamaVisionBackend
- LlamaCppVisionBackend
- OpenAICompatibleVisionBackend
- GeminiVisionBackend

- OllamaTextBackend
- LlamaCppTextBackend
- OpenAICompatibleTextBackend
- GeminiTextBackend

The rest of the pipeline must not depend on provider-specific response formats.

---

# 23. Escalation Strategy

Small local models should handle normal cases.

Escalate only when necessary.

Possible escalation triggers:

- low model confidence
- disagreement across overlapping crops
- dense page with too many unresolved nodes
- tiny text
- unusual design system
- ambiguous icons
- complex nested components
- very long page
- unrecognized logo
- failed JSON/schema compliance

Example policy:

```text
if page_confidence < 0.80
or unresolved_components > threshold
or crop_disagreement_rate > threshold
or schema_failures > 0
then escalate relevant regions
```

Prefer region-level escalation instead of resending the entire screenshot.

---

# 24. Optional Multi-Pass Voting

For difficult crops, the same small model can be run more than once.

Useful variants:

- tight crop
- medium crop
- crop with surrounding context

Agreement across passes increases confidence.

Disagreement becomes an escalation signal.

This does not require multiple 3B models in memory simultaneously.

---

# 25. CLI Design

Refraict should remain CLI-first.

Recommended commands:

```text
refraict analyze <image>
refraict regions <image>
refraict ocr <image>
refraict inspect <image-or-crop>
refraict merge <analysis-dir>
refraict summarize <analysis-dir>
refraict reconstruct <analysis-dir>
refraict benchmark <dataset-dir>
refraict cache status
refraict cache clear
```

## Primary command

```bash
refraict analyze screenshot.png
```

Expected behavior:

1. create analysis workspace
2. detect page dimensions
3. generate overview
4. run OCR
5. propose regions
6. create adaptive crops
7. analyze crops with local VLM
8. merge overlapping observations
9. sample colors and deterministic features
10. construct UI IR
11. generate region descriptions
12. generate page summary
13. construct UI graph
14. optionally infer probable DOM
15. write output artifacts

## Useful flags

```text
--vision-model
--summary-model
--aggregator-model
--vision-provider
--summary-provider
--aggregator-provider
--crop-size
--crop-overlap
--min-text-height
--batch-size
--workers
--no-ocr
--no-summary
--no-dom
--cloud-fallback
--output
--json
--verbose
```

---

# 26. Configuration Example

```yaml
vision:
  provider: ollama
  model: qwen-vl-3b
  endpoint: http://localhost:11434
  workers: 1
  batch_size: 2

summary:
  provider: ollama
  model: qwen-3b

aggregator:
  provider: ollama
  model: qwen-14b

image:
  overview_width: 1000
  crop_long_side: 1280
  crop_overlap: 0.20
  minimum_text_height_after_resize: 12

analysis:
  confidence_threshold: 0.80
  generate_dom_guess: true

cache:
  enabled: true
  database: ./refraict-cache.sqlite
```

---

# 27. Error Handling

Refraict must fail gracefully.

Expected cases:

## Model unavailable

- report backend failure
- preserve completed deterministic analysis
- return partial artifacts

## Invalid model JSON

- retry with schema-repair prompt once
- if still invalid, store raw model output
- mark the crop unresolved

## OCR failure

- continue with VLM-only analysis
- mark text confidence lower

## Crop analysis failure

- retry crop
- optionally enlarge context
- optionally escalate

## Unsupported image

- convert where possible
- otherwise return a clear validation error

## Out-of-memory

- reduce batch size automatically if safe
- retry sequentially
- never silently discard analysis

---

# 28. Performance Principles

Prioritize total throughput and repeatability over micro-optimizing Go code.

Most runtime cost will come from inference, OCR, and image decoding.

Recommended initial optimization order:

1. avoid unnecessary inference
2. cache aggressively
3. reduce crop count intelligently
4. keep model loaded
5. use small batches
6. avoid oversize images
7. preserve text readability
8. parallelize CPU preprocessing
9. optimize deterministic code only after profiling

---

# 29. Benchmark Dataset

Refraict should have a permanent evaluation corpus.

Include at least:

- simple login page
- SaaS landing page
- pricing page
- ecommerce listing
- admin dashboard
- analytics dashboard
- dense table
- settings page
- mobile UI screenshot
- very long scrolling page
- dark-mode UI
- unusual visual design
- screenshot with tiny text
- screenshot with charts
- screenshot with multiple logos/icons

For each benchmark image, maintain ground truth where practical.

---

# 30. Core Metrics

Measure at least:

## OCR

- text recall
- text precision
- character/word error rate

## Component detection

- component recall
- component precision
- type accuracy

## Geometry

- bounding-box IoU
- alignment correctness
- containment correctness

## Colors

- RGB/HEX error distance
- primary color identification accuracy

## Hierarchy

- parent/child accuracy
- same-row/same-column accuracy
- region classification accuracy

## Semantic output

- useful component-role accuracy
- repeated-component detection accuracy
- page-type accuracy

## Description quality

Evaluate whether a downstream text-only model can answer questions such as:

- What is the page layout?
- What are the main sections?
- What components repeat?
- What is the primary CTA?
- What is the visual style?

without needing access to the original screenshot.

---

# 31. Initial Acceptance Criteria

The following criteria define a useful first production-quality milestone.

## CLI

- `refraict analyze <image>` works end-to-end.
- Runs without requiring a GUI.
- Produces deterministic exit codes.
- Supports at least PNG and JPEG input.

## Portability

- Builds for Linux amd64.
- Builds for macOS arm64.
- Windows support is desirable for the first milestone and required before stable release.

## Image handling

- Can ingest screenshots at least 6000 px wide or 15000 px tall without attempting to send the entire full-resolution image directly to the local VLM.
- Generates a low-resolution overview.
- Generates adaptive crops.
- Uses overlapping crops.
- Does not reduce ordinary detected text below the configured minimum height when a subdivision can avoid it.

## OCR

- OCR output contains text, global bounding boxes, and confidence.
- OCR results are reusable by crop analyzers.

## Vision analysis

- A single persistent local 3B–4B VLM can analyze all crops sequentially.
- Model restart is not required between crops.
- Every successful crop produces structured output and a semantic description.

## Structured output

- Every component has a stable ID.
- Every component has global coordinates.
- Important properties include provenance.
- Invalid model responses do not crash the entire run.

## Colors

- Reported exact component colors come from deterministic sampling where possible.
- VLM color guesses are never silently represented as measured colors.

## Reconciliation

- Duplicate components from overlapping crops are merged.
- Conflicting observations preserve provenance/confidence.

## Summaries

- Every major region receives a natural-language summary.
- Every analyzed page receives a page-level Markdown summary.
- The page summary is usable without understanding the internal JSON schema.

## UI graph

- Refraict produces a hierarchy or graph describing major regions and component relationships.
- Probable DOM output is clearly marked as inferred rather than observed.

## Caching

- Re-running analysis on an unchanged image does not rerun cached OCR and crop-VLM inference unless relevant version keys changed.

## Diagnostics

- `--verbose` reports major stage timing.
- Errors identify the stage and affected crop/region.

---

# 32. Quality Acceptance Criteria

For an initial benchmark set of representative UI screenshots, target:

- at least 90% recall for clearly visible major UI regions
- at least 85% recall for ordinary visible controls larger than the minimum target size
- at least 90% correct OCR association for clearly readable labels
- median bounding-box IoU of at least 0.75 for detected major components
- measured dominant component colors within a small perceptual/RGB distance from ground truth
- at least 90% correct identification of high-level page type on the benchmark set
- at least 85% correct major parent/child grouping
- region/page descriptions judged sufficient for a downstream text-only model to identify the page's main structure and component inventory

These numbers should be adjusted after real benchmarking rather than treated as permanent truths.

---

# 33. Performance Acceptance Criteria

The first implementation should demonstrate that:

- the local VLM remains loaded across crop runs
- changing only the final summarization logic does not trigger new vision inference
- batch size can be configured independently from CPU preprocessing concurrency
- an OOM during batched inference can fall back to a smaller batch or sequential mode
- deterministic preprocessing is significantly cheaper than vision inference and does not become the primary bottleneck on ordinary screenshots

Do not set hard throughput numbers until target hardware is defined.

---

# 34. Suggested Implementation Phases

## Phase 1 — skeleton

Build:

- Go CLI
- configuration
- image ingest
- hashing
- workspace layout
- Ollama adapter
- simple fixed overlapping crops
- crop-level JSON output

Acceptance:

```bash
refraict analyze screenshot.png
```

produces crop analyses successfully.

## Phase 2 — deterministic evidence

Add:

- OCR
- global coordinates
- pixel color sampling
- overview generation
- resize/text-height logic

Acceptance:

Each crop analysis can reference OCR and deterministic color data.

## Phase 3 — normalization

Add:

- canonical UI IR
- provenance
- confidence
- duplicate merging
- relationships

Acceptance:

Overlapping crops merge into one coherent page representation.

## Phase 4 — descriptions

Add:

- crop `.md`
- region `.md`
- page `.md`
- 3B text summarizer adapter

Acceptance:

A text-only AI can understand the major structure of the screenshot using only Refraict's Markdown output.

## Phase 5 — intelligent regions

Replace fixed tiling with:

- OCR density
- whitespace segmentation
- edge/separator detection
- adaptive region subdivision

Acceptance:

Crop count decreases or semantic coverage improves relative to fixed tiling on the benchmark set.

## Phase 6 — UI graph and inferred DOM

Add:

- graph construction
- parent/child relationships
- reusable component recognition
- optional probable DOM representation

Acceptance:

Major layout hierarchy is reconstructed correctly on most benchmark pages.

## Phase 7 — escalation

Add:

- confidence thresholds
- disagreement detection
- region-level retries
- optional stronger local/cloud model

Acceptance:

Hard cases can be escalated without reprocessing the entire page.

## Phase 8 — benchmarking and optimization

Add:

- benchmark command
- evaluation corpus
- timing metrics
- model comparison
- crop-strategy comparison
- regression reports

Acceptance:

Prompt, model, or crop changes can be compared quantitatively.

---

# 35. Recommended First Model Strategy

Start simple.

## Vision

Use one local 3B–4B multimodal model.

Responsibilities:

- local semantics
- grouping
- component classification
- visual interpretation

## Summarizer

Use one local ~3B text model.

Responsibilities:

- crop-to-region condensation
- region-to-page condensation
- semantic descriptions

## Aggregator

Initially, try the same 3B model.

Only move to 7B–14B if benchmark results show a meaningful improvement in hierarchy reconstruction.

This prevents premature hardware requirements.

---

# 36. Security and Privacy

Refraict should default to local processing.

Cloud escalation should be explicit and configurable.

When cloud fallback is enabled:

- record which crop was uploaded
- record which provider/model received it
- allow disabling cloud entirely
- never silently upload screenshots

Potential future flags:

```text
--local-only
--allow-cloud
--redact-text-before-cloud
```

---

# 37. Design Principles to Preserve

As Refraict evolves, keep these principles stable:

### 1. Pixels are evidence.

If a property can be measured, measure it rather than asking a model to guess.

### 2. Small models should solve small problems.

Do not force a 3B VLM to reason over an enormous screenshot if the image can be decomposed.

### 3. Context should be hierarchical.

Maintain crop, region, and page levels.

### 4. Natural language is an output format, not merely debug text.

Descriptions should be usable directly by other AI systems.

### 5. Never discard provenance.

Know whether a fact came from OCR, CV, pixels, a VLM, or an aggregator.

### 6. Cache expensive work.

Prompt iteration should not force unnecessary vision recomputation.

### 7. Inference should be replaceable.

Ollama is an implementation detail, not the architecture.

### 8. Uncertainty is data.

Conflicting model outputs should trigger lower confidence or escalation, not silent guessing.

### 9. The UI IR is the contract.

Model output should be normalized into Refraict's schema.

### 10. Benchmark before scaling model size.

A better decomposition pipeline may outperform a larger model at far lower cost.

---

# 38. Definition of Done for Refraict v0.1

Refraict v0.1 is considered complete when a user can run:

```bash
refraict analyze screenshot.png --output ./analysis
```

and receive:

```text
analysis/
├── page.json
├── page.md
├── graph.json
├── overview.png
├── evidence/
├── regions/
└── crops/
```

where:

- `page.json` contains normalized structured UI information
- `page.md` contains an AI-readable description of the full interface
- `graph.json` contains major component hierarchy and relationships
- deterministic evidence contains OCR, geometry, and color data
- region/crop outputs preserve detailed local observations
- all coordinates resolve to the original screenshot
- all important values preserve provenance/confidence
- the VLM stays warm across crop analysis
- cached analyses are reused on subsequent runs
- the entire process can run locally without a cloud service

That is the minimum foundation upon which more sophisticated DOM reconstruction, code generation, comparison, and design-system extraction can be built.

---

# 39. Final Architecture Summary

```text
                    ultra-high-resolution screenshot
                                │
                                ▼
                    immutable original + hash
                                │
                   ┌────────────┴────────────┐
                   ▼                         ▼
                 OCR                    image/CV
                   │                         │
                   └────────────┬────────────┘
                                ▼
                        adaptive region plan
                                │
                                ▼
                  overlapping multi-scale crops
                                │
                                ▼
                     persistent 3B–4B VLM
                                │
                                ▼
                   crop JSON + crop descriptions
                                │
                                ▼
               normalize / dedupe / global coordinates
                                │
                                ▼
                           Refraict UI IR
                                │
                ┌───────────────┼────────────────┐
                ▼               ▼                ▼
             evidence       UI graph       3B summarizer
                │                                │
                │                                ▼
                │                         region summaries
                │                                │
                │                                ▼
                │                           page summary
                │                                │
                └───────────────┬────────────────┘
                                ▼
                       optional larger aggregator
                                │
                                ▼
                  probable DOM / reusable components
                                │
                                ▼
                     optional selective escalation
```

Refraict should be built as a **measurement-first, small-model-first, hierarchical analysis system**, not as a single giant vision prompt.
