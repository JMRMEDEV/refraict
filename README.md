# Refraict

**Refraict** converts UI screenshots (web, desktop, mobile) into reusable, structured, AI-friendly context. It applies deterministic computer vision + OCR to measure what is directly measurable, then small local models (vision + text) to add semantic interpretation — producing both machine-readable JSON and natural-language summaries suitable for another AI agent to consume.

This document is both a **reference** and a **user guide**.

---

## Table of Contents

1. [What Refraict does](#what-refraict-does)
2. [Installation & build](#installation--build)
3. [Quick start](#quick-start)
4. [Command reference](#command-reference)
   - [Global flags](#global-flags)
   - [analyze](#analyze)
   - [ocr](#ocr)
   - [regions](#regions)
   - [inspect](#inspect)
   - [merge](#merge)
   - [summarize](#summarize)
   - [reconstruct](#reconstruct)
   - [cache](#cache)
   - [benchmark](#benchmark)
   - [version](#version)
5. [Configuration](#configuration)
6. [Output layout & artifacts](#output-layout--artifacts)
7. [Backends: Ollama & cloud](#backends-ollama--cloud)
8. [Caching](#caching)
9. [End-to-end example](#end-to-end-example)
10. [FAQ / troubleshooting](#faq--troubleshooting)
11. [Documentation](#documentation)

---

## What Refraict does

Given a screenshot, Refraict:

- Detects and dissects the image deterministically (dimensions, dominant colors, hash).
- Runs OCR to pull visible text tokens with bounding boxes (auto-inverts dark-theme UIs for legibility).
- Plans a bounded set of **crops** (overview + grid of focused tiles) optimized for small vision models.
- Synthesizes **components deterministically** from measured evidence: OCR text tokens become text components, and a CV detector finds non-text regions (cards, panels, chart containers). Coordinates and colors are measured, not guessed.
- Types non-text regions (icon/logo/chart/image) and, for graphic elements, adds a **vote-based semantic label**: the vision model is sampled multiple times per element, canonicalized against an embedded Lucide alias map, and majority-voted — a label is kept only when the runs agree (confidence = agreement ratio), else the element stays unlabeled.
- Uses a **vision model** on each crop only for a *grounded* natural-language description (constrained to the measured OCR text and colors), not for geometry — small local models cannot emit reliable bounding boxes.
- Reconciles overlapping observations into one deduplicated component set.
- Measures actual pixel colors for each component.
- Builds a canonical **UI IR** (`page.json`) plus a spatial **relationship graph** (`graph.json`).
- Generates **region-level** and **page-level** natural-language summaries, then runs a deterministic **grounding guard** that flags summary claims (colors, numbers, quoted text, non-observable behavior) unsupported by the measured evidence.
- Infers a **probable DOM/UI tree** (clearly marked as inference, not observed).

### Design principles

> Use deterministic tools for facts that can be measured directly, small vision models for local semantic interpretation, small text models for compression and synthesis, and larger models only when global reasoning or escalation is necessary.

- **Local-first, low cost** — defaults to Ollama, nothing leaves the machine.
- **Cross-platform** — a Go executable for Linux, macOS, and Windows (requires system OpenCV 4.x at build and runtime; see Installation).
- **Efficient with small models** — adaptive cropping avoids feeding huge images to 3B-class models.
- **Provenance & confidence** — every important property records source and confidence.
- **Graceful degradation** — if OCR or a vision backend is unavailable, analysis still runs (with warnings), producing the deterministic pieces.

---

## Installation & build

Refraict requires **Go 1.22+** and **OpenCV 4.x** (with headers). OpenCV is a
hard dependency: refraict's region detection (cards, panels, containers — which
underpin the structural signals in the output) is OpenCV-backed via CGo. Install
OpenCV first for your platform:

```bash
# Debian / Ubuntu
sudo apt-get install -y libopencv-dev pkg-config

# Fedora
sudo dnf install -y opencv-devel pkgconf-pkg-config

# macOS (Homebrew)
brew install opencv pkg-config

# Arch
sudo pacman -S opencv pkgconf
```

Then build:

```bash
git clone <repo-url> refraict
cd refraict
go build -o refraict ./cmd/refraict
```

The build links CGo against system OpenCV, so the resulting binary is **not**
statically linked and needs OpenCV present at runtime. Verify it works:

```bash
./refraict version
./refraict inspect <some-screenshot.png>   # deterministic, no models needed
```

> **Note on gocv/OpenCV versions:** refraict pins `gocv.io/x/gocv` (see `go.mod`);
> gocv supports specific OpenCV releases. If the build fails to find OpenCV,
> confirm `pkg-config --modversion opencv4` reports a 4.x version and that the
> gocv version in `go.mod` matches your OpenCV. Region detection is controlled by
> `analysis.detect_regions` (default on).

Run the test suite:

```bash
go test ./...
go vet ./...
```

---

## Quick start

```bash
# 0. (optional) start a local Ollama server with the models
ollama serve &
ollama pull gemma3:4b     # vision
ollama pull qwen2.5:3b    # summary/aggregator (text)

# 1. Analyze a screenshot (offline-first version works without models too)
./refraict analyze my-app.png --output ./out

# 2. Read the outputs
cat ./out/page.md        # natural-language page summary
cat ./out/regions/*.md   # per-region summaries
cat ./out/page.json      # canonical UI IR (JSON)
cat ./out/dom.md         # inferred probable DOM
```

To get the full semantic pipeline (vision + summaries), make sure a vision model is reachable:

```bash
./refraict analyze my-app.png --config ./refraict.json --output ./out --verbose
```

---

## Command reference

### Global flags

Use `./refraict --help` to see the complete command set.

| Flag | Alias | Description |
| --- | --- | --- |
| `--config <path>` | `-c` | Path to a JSON config file. If omitted, defaults are used. |
| `--verbose` | `-v` | Enable verbose diagnostics (per-stage timing, warnings). |
| `--json` | — | Emit machine-readable JSON where applicable. |

```bash
./refraict --help
./refraict <command> --help
```

### `analyze`

**`analyze <image>`** — Run the full end-to-end analysis pipeline on a screenshot.

```bash
./refraict analyze screenshot.png --output ./out
```

Flags:

| Flag | Description | Default |
| --- | --- | --- |
| `--vision-model` | Vision model name (overrides config) | from config |
| `--summary-model` | Summary text model name | from config |
| `--aggregator-model` | Aggregator model name | from config |
| `--vision-provider` | Vision provider (`ollama`, …) | `ollama` |
| `--summary-provider` | Summary provider | `ollama` |
| `--aggregator-provider` | Aggregator provider | `ollama` |
| `--crop-size` | Crop longest side in px | `1280` |
| `--crop-overlap` | Crop overlap (0–1) | `0.20` |
| `--min-text-height` | Min text height after resize (px) | `12` |
| `--batch-size` | Inference batch size | from config |
| `--workers` | Inference workers | from config |
| `--no-ocr` | Skip the OCR stage | `false` |
| `--no-summary` | Skip summaries | `false` |
| `--no-dom` | Skip DOM guess | `false` |
| `--cloud-fallback` | Allow cloud escalation | `false` |
| `--output`, `-o` | Output directory | `./analysis-<basename>` |
| `--adaptive` | Use adaptive crop planning (see crop strategies) | `true` |
| `--keep-warm` | Keep local models loaded for this duration after use (e.g. `30s`, `5m`, `-1` = indefinite). Empty frees them immediately. | (free immediately) |

On success it prints:

```
Analysis complete: ./out (2.1s), 4 crops, 12 components.
```

### `icons`

**`icons <image>`** — Detect and identify non-text UI elements (icons, logos,
charts) without running the full pipeline. For each graphic element it prints
the type, bounding box, and — unless `--no-label` — a vote-based semantic label
with its agreement ratio.

```bash
# Identify icons (vote-based labeling)
./refraict icons screenshot.png --config ./refraict.json

# Fast, model-free: just detect + type, and dump the exact crops the VLM sees
./refraict icons screenshot.png --dump-crops ./crops --no-label
```

Flags:

| Flag | Description | Default |
| --- | --- | --- |
| `--dump-crops <dir>` | Write each element's VLM crop PNG to a directory (for inspection/tuning) | — |
| `--no-label` | Skip VLM labeling (detect + type + optional dump only; no model) | `false` |
| `--runs` | VLM samples per element for voting | from config |
| `--threshold` | Min vote agreement ratio to accept a label | from config |
| `--pad` | Fractional padding around each element before cropping (smaller = tighter to the icon) | from config |
| `--vision-model` | Vision model name | from config |
| `--keep-warm` | Keep the model loaded for this duration after use | free immediately |

Non-text element detection uses refraict's OpenCV-backed region detector
(cards, panels, icons), including a CLAHE pass that recovers faint cards on
low-contrast/light-theme UIs.

### `ocr`

**`ocr <image>`** — Run OCR on an image and print tokens as JSON.

```bash
./refraict ocr screenshot.png
```

Output: `{"tokens": [...], "count": N}`. If no OCR engine is configured, it prints a warning and an empty result.

OCR is optional and uses an **external command** driven by two environment variables:

| Variable | Purpose |
| --- | --- |
| `REFRAICT_OCR_CMD` | Executable that performs OCR on an image and prints a JSON array of tokens to stdout. If unset, OCR is skipped (VLM-only analysis still runs). |
| `REFRAICT_OCR_ARGS` | Optional space-separated fixed arguments passed to the OCR command (**before** the image path). |

The OCR command receives the input image path as its final argument (`REFRAICT_OCR_CMD [REFRAICT_OCR_ARGS...] <image>`). Its stdout must be a JSON array of token objects:

```json
[
  {"text": "Submit", "bbox": [100, 200, 240, 220], "confidence": 0.99}
]
```

`bbox` is the token's bounding box in **global image coordinates** as `[x0, y0, x1, y1]`. Example with a RapidOCR/PaddleOCR-style CLI:

```bash
export REFRAICT_OCR_CMD="ocr-infer"
./refraict analyze screenshot.png
```

OCR tokens are cached per image, used to steer the adaptive crop plan, appended to crop prompts, and (when a crop's vision output is broken) recovered via text-token matching in the repair stage. OCR degrades gracefully — without it, the deterministic pieces (overview, colors, geometry) are still produced.

### `regions`

**`regions <image>`** — Print the proposed crop/region plan as JSON.

```bash
./refraict regions screenshot.png
# {"crops": [...], "count": 4, "width": 1600, "height": 1200}
```

### `inspect`

**`inspect <image-or-crop>`** — Show deterministic facts (dimensions, colors, hash) for an image or crop, without any model.

```bash
./refraict inspect screenshot.png
# path: screenshot.png
# width: 1600
# height: 1200
# sha256: ...
# format: png
# dominant_color: #f0f2f5

./refraict inspect screenshot.png --json
```

### `merge`

**`merge <analysis-dir>`** — Reconcile overlapping crop components from an existing analysis directory. Re-reads `<dir>/crops/*.json`, dedupes by IoU, and writes `<dir>/evidence/merged_components.json`.

```bash
./refraict merge ./out
```

### `summarize`

**`summarize <analysis-dir>`** — Regenerate region + page summaries from an existing analysis. Re-reads `<dir>/crops/*.md` and writes `<dir>/page.md`.

```bash
./refraict summarize ./out
```

### `reconstruct`

**`reconstruct <analysis-dir>`** — Build a probable DOM/UI tree from an existing analysis (reads `evidence/merged_components.json`). Writes `dom.md` and prints the tree.

```bash
./refraict reconstruct ./out
```

### `cache`

Inspect or clear the analysis cache.

```bash
./refraict cache status     # cache entries + root
./refraict cache status --json
./refraict cache clear
```

### `benchmark`

**`benchmark <dataset-dir>`** — Run a deterministic benchmark evaluation over a directory of images (`.png`, `.jpg`, `.jpeg`), reporting geometry and region counts per file.

```bash
./refraict benchmark ./dataset
```

### `version`

Print the version.

```bash
./refraict version
# refraict v0.1.0
```

---

## Configuration

Refraict reads a JSON config file (`--config refraict.json`). Any omitted fields fall back to built-in defaults. Example:

> **Hardware presets:** ready-made configs for common hardware live in
> [`presets/`](presets/) — `gpu-8gb.json` (recommended default), `gpu-4gb.json`,
> `gpu-16gb.json`, and `cpu-only.json`. Use one with
> `--config presets/gpu-8gb.json`. See [`presets/README.md`](presets/README.md).

```json
{
  "vision": {
    "provider": "ollama",
    "model": "gemma3:4b",
    "endpoint": "http://localhost:11434",
    "workers": 1,
    "batch_size": 1
  },
  "summary": {
    "provider": "ollama",
    "model": "qwen2.5:3b",
    "endpoint": "http://localhost:11434"
  },
  "aggregator": {
    "provider": "ollama",
    "model": "qwen2.5:3b",
    "endpoint": "http://localhost:11434"
  },
  "models": {
    "keep_alive": "0"
  },
  "image": {
    "overview_width": 1000,
    "crop_long_side": 1280,
    "crop_overlap": 0.20,
    "minimum_text_height_after_resize": 12,
    "detail_long_side": 1100,
    "crop_strategy": "grid",
    "grid_rows": 2,
    "grid_cols": 2
  },
  "analysis": {
    "confidence_threshold": 0.80,
    "generate_dom_guess": true,
    "detect_regions": true,
    "label_elements": true,
    "max_element_labels": 12,
    "element_label_runs": 10,
    "element_label_threshold": 0.7,
    "element_label_pad_frac": 0.15,
    "no_ocr": false,
    "no_summary": false
  },
  "cache": {
    "enabled": true,
    "dir": "./.refraict-cache"
  },
  "cloud": {
    "enabled": false,
    "local_only": true,
    "allow_cloud": false,
    "redact_text_before_cloud": true
  },
  "output": {
    "verbose": false,
    "json": false
  },
  "reconciler": {
    "iou_threshold": 0.65,
    "confidence_merge": 0.5
  }
}
```

### Config sections

- **`vision`** — vision model backend (provider, model, endpoint, workers, batch size).
- **`summary`** — small text model used for region/page summarization.
- **`aggregator`** — larger text model for global reasoning/escalation.
- **`models`** — cross-cutting local-model runtime settings. `keep_alive` is the Ollama keep-alive applied to every model request. Default `"0"` frees each model from memory immediately after use (only one model resident at a time — lowest RAM/VRAM). Set a duration (`"30s"`, `"5m"`) or `"-1"` (indefinite) for batch/agentic callers that prefer to trade memory for reduced reload latency. The `--keep-warm` flag overrides this per run.
- **`vision.profile`** (optional) — per-model output-handling overrides. Small VLMs produce differently-shaped noise, so Refraict resolves an output *profile* from the vision model name (verbosity cap, whether the model cites hex color codes in prose, structured-output support) — see `internal/modelprofile`. Override any field here: `max_label_words`, `strip_hex_in_numbers`, `structured_output`. Omit to use the name-resolved defaults.
- **`image`** — ingest + crop-planning parameters. `crop_strategy` selects the crop planner: `"grid"` (default; bounded overview + `grid_rows`×`grid_cols` focused tiles — fast, OCR-independent, keeps a single model warm within a run) or `"adaptive"` (legacy OCR-density-driven subdivision, which can explode the crop count on text-dense pages).
- **`analysis`** — confidence threshold, DOM-guess toggle, stage toggles, `detect_regions` (default `true`) which enables deterministic non-text region detection (cards, panels, chart containers), and `label_elements` (default `true`) which adds vote-based grounded labels to graphic regions (icon/logo/chart/image). Each such element is sampled by the vision model `element_label_runs` times (default `10`); the outputs are canonicalized against an embedded Lucide-derived alias map (TF-IDF weak-flagged) and majority-voted. A label is written to the component's `semantic` field (source `vlm_element_vote`, confidence = vote agreement ratio) **only** when agreement ≥ `element_label_threshold` (default `0.7`); otherwise the element is detected but left unlabeled. The relatively high default favors precision over recall: it withholds low-agreement guesses (e.g. a document icon the model reads as "credit card" at 6/10) rather than emitting a confident-wrong label. `max_element_labels` (default `12`) bounds VLM calls per analysis. Region detection is OpenCV-backed (Canny + a CLAHE dual-pass for faint/low-contrast cards).
- **`cache`** — cache enablement and database directory.
- **`cloud`** — cloud escalation policy (disabled by default; text is redacted before any cloud call).
- **`output`** — global verbosity / JSON defaults.
- **`reconciler`** — IoU threshold and confidence merge used to dedupe overlapping crop components.

---

## Output layout & artifacts

`analyze` writes a workspace directory with this layout:

```
out/
├── manifest.json              # image path, sha256, dimensions, timestamp
├── overview.png               # resized overview image
├── page.json                  # CANONICAL UI IR (components, colors, relationships, summary, provenance)
├── page.md                    # page-level natural-language summary
├── graph.json                 # spatial relationship graph
├── dom.json                   # inferred DOM (marked inferred:true)
├── dom.md                     # probable DOM tree (inference)
├── crops/
│   ├── <id>.json              # raw per-crop vision result (components, bbox, confidence)
│   └── <id>.md                # per-crop description
├── regions/
│   └── <id>.md                # per-region natural-language summary
└── evidence/
    ├── ocr.json               # OCR tokens
    ├── regions.json           # planned crop regions
    ├── merged_components.json # deduplicated components (OCR text + CV regions)
    ├── colors.json            # measured pixel colors per component
    └── grounding.json         # grounding-guard report (claims unsupported by evidence)
```

**`page.json`** is the key reusable artifact — feed it to any LLM as structured UI context. It includes schema version, components, colors, relationship elements, the page summary, and full provenance of every model backend.

---

## Backends: Ollama & cloud

**Ollama** (default, recommended) — local and free. Pull models that fit your
hardware (examples that run on modest GPUs):

```bash
ollama serve &
ollama pull gemma3:4b     # vision (~3.3 GB, fits ~8 GB VRAM; best measured for icons/UI)
ollama pull qwen2.5:3b    # summary + aggregator (text)
```

Ollama exposes an OpenAI-compatible API, so Refraict speaks to it over HTTP at
the configured endpoint (default `http://localhost:11434`). Configure model
names per role under `vision`, `summary`, and `aggregator` in the config file.
By default each model is freed from memory immediately after use
(`models.keep_alive: "0"`); pass `--keep-warm=<duration>` for batch runs.

**Cloud** — cloud escalation is **disabled by default** for cost and privacy.
Refraict is local-first; cloud is only ever used to escalate the final
page-level aggregation when local confidence is low, and only if you opt in. The
`cloud` config controls this:

| Field | Meaning | Default |
| --- | --- | --- |
| `cloud.enabled` | Master switch for the cloud backend. | `false` |
| `cloud.local_only` | Hard guarantee that nothing leaves the machine; overrides escalation even if allowed. | `true` |
| `cloud.allow_cloud` | Permit escalation to a cloud model when escalation signals warrant it. | `false` |
| `cloud.redact_text_before_cloud` | Redact detected text before any cloud request. | `true` |

With the defaults (`enabled:false`, `local_only:true`), Refraict never contacts
a cloud service — everything runs locally. To use cloud escalation you must set
`enabled:true`, `local_only:false`, and `allow_cloud:true`; even then, text is
redacted first when `redact_text_before_cloud` is on. The `--cloud-fallback`
flag enables escalation for a single run without editing config.

---

## MCP server (for AI agents)

Refraict ships an [MCP](https://modelcontextprotocol.io) server
(`cmd/refraict-mcp`) that exposes the pipeline to AI agents over stdio. It runs
the **same in-process pipeline** as `refraict analyze` (no subprocess) and
returns a **bounded summary + pointers** to the on-disk artifacts rather than
dumping the large `page.json` into the agent's context.

Build it (requires OpenCV 4.x, like the main binary):

```bash
go build -o refraict-mcp ./cmd/refraict-mcp
```

Tools:

| Tool | Purpose |
| --- | --- |
| `analyze` | Run the full pipeline on an image. Returns a bounded summary — page type + confidence, component counts by type, repeated-group count, `corner_styles` (rounded/square per card), container `paddings`, repeated-group `group_spacing` gaps, and the `grounding` / `crosscheck` / `consolidation_check` scores — plus `output_dir` and `artifacts` paths. |
| `inspect` | Deterministic facts (dimensions, SHA-256, format, dominant color). No models; fast. |
| `get_artifact` | Read back a named artifact (`page_json`, `graph_json`, `page_md`, `page_consolidated`, `dom_md`, `grounding`, `crosscheck`, `merged_components`, `colors`, `ocr`) from a prior `analyze` `output_dir` — pull full detail on demand. |

The design keeps the decision signals (page type, grounding, crosscheck) in the
`analyze` response so the agent can decide whether to trust the summary or pull
the full artifacts; the heavy data stays on disk. OCR (`REFRAICT_OCR_CMD`) and
Ollama are used the same way as the CLI. Register it with any MCP client, e.g.:

```json
{
  "mcpServers": {
    "refraict": {
      "command": "/path/to/refraict-mcp",
      "env": { "REFRAICT_OCR_CMD": "ocr-infer" }
    }
  }
}
```

## Caching

Refraict caches every expensive, reproducible stage, keyed by image SHA + stage + model:

- OCR results (`ocr-v1`).
- Per-crop vision inference (`vision-v1` + model name).

Re-running `analyze` on an unchanged image is near-instant because cached crop/OCR results are reused (unless the model or relevant version keys change).

- Cache location defaults to `./.refraict-cache` (config: `cache.dir`; legacy `cache.database` key is still accepted).
- Inspect/clear with `./refraict cache status` / `./refraict cache clear`.

---

## End-to-end example

```bash
# 1. Create a screenshot (example: programmatically with Pillow)
python3 - << 'EOF'
from PIL import Image, ImageDraw
img = Image.new('RGB', (1200, 800), '#ffffff')
d = ImageDraw.Draw(img)
d.rectangle([40, 40, 1160, 100], fill='#2563eb')
d.text((60, 60), "Login", fill='white')
for i, label in enumerate(["Username", "Password"]):
    d.rectangle([100, 160 + i*100, 700, 220 + i*100], fill='#f1f5f9', outline='#cbd5e1')
    d.text((120, 170 + i*100), label, fill='#0f172a')
d.rectangle([100, 400, 420, 470], fill='#2563eb')
d.text((180, 420), "Sign in", fill='white')
img.save('/tmp/login.png')
EOF

# 2. Build and analyze
go build -o refraict ./cmd/refraict
./refraict analyze /tmp/login.png --output /tmp/out --verbose

# 3. Inspect everything
cat /tmp/out/page.md
cat /tmp/out/dom.md
cat /tmp/out/evidence/merged_components.json
jq '.components' /tmp/out/page.json
```

Expected prints from the pipeline:

```
[stage] image       1ms
[stage] overview    5ms
[stage] ocr         300ms (or warning)
[stage] crop-plan   2ms
[stage] vision      1.2s (or warning "no vision backend")
[stage] merge+graph 3ms
[stage] summary     ...
[stage] dom         1ms
Analysis complete: /tmp/out (2.1s), 2 crops, 6 components.
```

---

## FAQ / troubleshooting

**"warning: no vision backend" — how do I enable semantic analysis?**
Start Ollama and pull a vision model (see [Backends](#backends-ollama--cloud)). Configure `vision` in the config file and pass `--config`. Without a vision backend, Refraict still produces the deterministic artifacts (overview, OCR, crop plan, colors, geometry).

**"ocr engine unavailable" — how do I enable OCR?**
Refraict shells out to an OCR engine configured via the environment/backend. If one isn't present it logs a warning and continues model-only. Check your OCR setup and that its output is reachable by `ocr.Input{ImagePath}`.

**Analyses re-run too fast / results look cached.**
That's the cache working. Use `./refraict cache clear` and re-run if you want a fresh pass (e.g., after changing a model or prompt version).

**Cost concerns with cloud?**
Cloud is disabled by default. Keep it disabled, stick with Ollama, and rely on `cloud.redact_text_before_cloud` if you ever enable it.

**I changed the crop size / overlap but output is identical.**
`analyze` flags override config at runtime (`--crop-size`, `--crop-overlap`). Also confirm `--adaptive` is on, or pass `--adaptive=true` explicitly.

**High RAM/VRAM usage with local models.**
By default Refraict frees each local model from memory immediately after use (`models.keep_alive: "0"`), so at most one model is resident at a time. If you run many analyses back-to-back and want to avoid repeated model reloads, pass `--keep-warm=5m` (or set `models.keep_alive`) to trade memory for speed. Note that on constrained VRAM, keeping both the vision and text models warm simultaneously can exceed the GPU and spill into system RAM.

**Region detection misses cards on a low-contrast/dark UI.**
Region detection is OpenCV-backed (Canny + CLAHE). Faint cards on very low-contrast/light-theme UIs are recovered by a CLAHE dual-pass, though extremely faint borders (card interior == page background) may still be missed. You can disable region detection with `analysis.detect_regions: false`.

---

## Documentation

All project documentation lives under [`docs/`](docs/):

- [`docs/refraict.md`](docs/refraict.md) — the implementation/spec guide: full architecture, IR schema, and design rationale.
- [`docs/roadmap/gaps-vs-vision-llm.md`](docs/roadmap/gaps-vs-vision-llm.md) — the gap roadmap and gap-closing history: how Refraict compares to a vision-LLM (`fs_read`) on text, color, layout, and non-text detection, the prioritized plan to narrow each gap, and a dated log of what has been implemented (OCR dark-theme fix, grounded summaries + grounding guard, CV region detection, keep-alive memory controls).
- [`docs/qa/`](docs/qa/) — QA findings and reassessments.

## License / contribution

See [`docs/refraict.md`](docs/refraict.md) for the full architecture, IR schema, and design rationale. Contributions, issues, and PRs are welcome.
