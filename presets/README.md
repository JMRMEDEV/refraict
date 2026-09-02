# Refraict hardware presets

Ready-made configuration files tuned for different hardware, so you don't have
to hand-tune. Pass one with `--config`:

```bash
./refraict analyze screenshot.png --config presets/gpu-8gb.json --output ./out
```

Any field you omit falls back to Refraict's built-in defaults, so these presets
only set what matters for the tier. All presets keep cloud **disabled** and
local-only (nothing leaves your machine).

## Choosing a preset

| Preset | Target hardware | Vision model | Notes |
| --- | --- | --- | --- |
| [`gpu-8gb.json`](gpu-8gb.json) | ~8 GB VRAM (validated) | `gemma3:4b` | The recommended default; measured on an 8 GB card. 2×2 crop grid, models freed after use (`keep_alive:0`). |
| [`gpu-4gb.json`](gpu-4gb.json) | ~4 GB VRAM / low-end | `moondream` | Smallest footprint: tiny vision model, 1×2 grid, fewer element-label samples. |
| [`gpu-16gb.json`](gpu-16gb.json) | 16 GB+ VRAM | `qwen2.5-vl:7b` | Quality-first: stronger VLM + 7B text model, 3×2 grid, models kept warm (`keep_alive:5m`) for faster batches. |
| [`cpu-only.json`](cpu-only.json) | No usable GPU | (unused) | Deterministic-only: OCR + measured colors + region detection + grounding guard. Summaries and element labels are OFF, so no vision/text model is needed. |

If unsure, start with `gpu-8gb.json`. On a constrained machine, drop to
`gpu-4gb.json`. If you have no GPU (or don't want to run models), use
`cpu-only.json` — it still produces `page.json` with components, colors,
geometry, and detected regions, just without model-written descriptions/labels.

## Pull the models first

The GPU presets expect these Ollama models (pull once):

```bash
ollama serve &
# gpu-8gb
ollama pull gemma3:4b
ollama pull qwen2.5:3b
# gpu-4gb (in addition)
ollama pull moondream
# gpu-16gb (in addition)
ollama pull qwen2.5-vl:7b
ollama pull qwen2.5:7b
```

`cpu-only.json` needs no models (with OCR via the external OCR command; see the
main README's OCR section).

## Memory notes

- All GPU presets set `workers: 1` and (except 16gb) `keep_alive: "0"` so at
  most one model is resident at a time — the safe choice on limited VRAM.
- For many back-to-back analyses, add `--keep-warm=5m` to trade memory for
  reduced model reload latency (this is what `gpu-16gb.json` does by default).

## Non-text region detection

`detect_regions` is on in every preset, but the detector is chosen at build
time: the default build uses a pure-Go detector; build with `-tags opencv`
(requires system OpenCV) for the stronger Canny detector that handles
low-contrast flat UIs. See the main README.

## Tuning knobs (per preset)

- `image.grid_rows` / `grid_cols` — crop coverage vs cost (more tiles = more
  detail, more VLM calls).
- `analysis.element_label_runs` — VLM samples per element for vote-based icon
  labeling (higher = more reliable, slower).
- `analysis.element_label_threshold` — min vote agreement to accept a label
  (higher = fewer but more trustworthy labels; 0.7 avoids confident-wrong).
- `models.keep_alive` — `"0"` frees models immediately; a duration keeps them
  warm for batches.
