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

---

## What Refraict does

Given a screenshot, Refraict:

- Detects and dissects the image deterministically (dimensions, dominant colors, hash).
- Runs OCR to pull visible text tokens with bounding boxes.
- Plans a set of **crops** (adaptive regions) optimized for small vision models.
- Uses a **vision model** on each crop to extract components (buttons, inputs, text, images…) with confidence and global coordinates.
- Reconciles overlapping per-crop observations into one deduplicated component set.
- Measures actual pixel colors for each component.
- Builds a canonical **UI IR** (`page.json`) plus a spatial **relationship graph** (`graph.json`).
- Generates **region-level** and **page-level** natural-language summaries.
- Infers a **probable DOM/UI tree** (clearly marked as inference, not observed).

### Design principles

> Use deterministic tools for facts that can be measured directly, small vision models for local semantic interpretation, small text models for compression and synthesis, and larger models only when global reasoning or escalation is necessary.

- **Local-first, low cost** — defaults to Ollama, nothing leaves the machine.
- **Portable** — a single Go executable for Linux, macOS, and Windows.
- **Efficient with small models** — adaptive cropping avoids feeding huge images to 3B-class models.
- **Provenance & confidence** — every important property records source and confidence.
- **Graceful degradation** — if OCR or a vision backend is unavailable, analysis still runs (with warnings), producing the deterministic pieces.

---

## Installation & build

Requires Go 1.22+.

```bash
git clone <repo-url> refraict
cd refraict
go build -o refraict ./cmd/refraict
```

The resulting `./refraict` binary has no runtime dependencies.

Cross-compile:

```bash
# macOS arm64
GOOS=darwin GOARCH=arm64 go build -o refraict-darwin-arm64 ./cmd/refraict
# Windows amd64
GOOS=windows GOARCH=amd64 go build -o refraict.exe ./cmd/refraict
```

Run the test suite:

```bash
go test ./...
go vet ./...
```

---

## Quick start

```bash
# 0. (optional) start a local Ollama server with a vision model
ollama serve &
ollama pull qwen-vl-3b

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
| `--adaptive` | Use adaptive crop planning | `true` |

On success it prints:

```
Analysis complete: ./out (2.1s), 4 crops, 12 components.
```

### `ocr`

**`ocr <image>`** — Run OCR on an image and print tokens as JSON.

```bash
./refraict ocr screenshot.png
```

Output: `{"tokens": [...], "count": N}`. If no OCR engine is configured, it prints a warning and an empty result.

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

```json
{
  "vision": {
    "provider": "ollama",
    "model": "qwen-vl-3b",
    "endpoint": "http://localhost:11434",
    "workers": 1,
    "batch_size": 2
  },
  "summary": {
    "provider": "ollama",
    "model": "qwen-3b",
    "endpoint": "http://localhost:11434"
  },
  "aggregator": {
    "provider": "ollama",
    "model": "qwen-14b",
    "endpoint": "http://localhost:11434"
  },
  "image": {
    "overview_width": 1000,
    "crop_long_side": 1280,
    "crop_overlap": 0.20,
    "minimum_text_height_after_resize": 12,
    "detail_long_side": 1100
  },
  "analysis": {
    "confidence_threshold": 0.80,
    "generate_dom_guess": true,
    "no_ocr": false,
    "no_summary": false
  },
  "cache": {
    "enabled": true,
    "database": "./refraict-cache.sqlite"
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
- **`image`** — ingest + crop-planning parameters.
- **`analysis`** — confidence threshold, DOM-guess toggle, stage toggles.
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
    ├── merged_components.json # deduplicated components
    └── colors.json            # measured pixel colors per component
```

**`page.json`** is the key reusable artifact — feed it to any LLM as structured UI context. It includes schema version, components, colors, relationship elements, the page summary, and full provenance of every model backend.

---

## Backends: Ollama & cloud

**Ollama** (default, recommended) — local and free:

```bash
ollama serve &
ollama pull qwen-vl-3b    # vision
ollama pull qwen-3b       # summary
ollama pull qwen-14b      # aggregator
```

Ollama exposes an OpenAI-compatible API, so Refraict speaks to it over HTTP at the configured endpoint (default `http://localhost:11434`).

**Cloud** — cloud escalation is **disabled by default** for cost and privacy. If you enable it, text is redacted before any cloud request. See the `cloud` config section.

---

## Caching

Refraict caches every expensive, reproducible stage, keyed by image SHA + stage + model:

- OCR results (`ocr-v1`).
- Per-crop vision inference (`vision-v1` + model name).

Re-running `analyze` on an unchanged image is near-instant because cached crop/OCR results are reused (unless the model or relevant version keys change).

- Cache location defaults to `./refraict-cache.sqlite` (config: `cache.database`).
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

---

## License / contribution

See `refraict.md` (the implementation/spec guide) for the full architecture, IR schema, and design rationale. Contributions, issues, and PRs are welcome.
