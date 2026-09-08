# Changelog

## [0.2.0](https://github.com/JMRMEDEV/refraict/compare/v0.1.0...v0.2.0) (2026-09-08)


### Features

* **analyze:** vote-based, threshold-gated element labeling ([3b5cb8f](https://github.com/JMRMEDEV/refraict/commit/3b5cb8f41a52b0b51a4e7efc5ff8974a1f5281d2))
* **analyze:** wire grounded pipeline, region detection, grid strategy, and keep-warm ([96085f4](https://github.com/JMRMEDEV/refraict/commit/96085f4212a0ece977b1f69d528fa0e332100c9e))
* **cli:** richer page-type classification with signals ([3f29f85](https://github.com/JMRMEDEV/refraict/commit/3f29f85d467e6e660c977b18d6596d145c377e23))
* **config:** raise element_label_threshold default to 0.7 (precision over recall) ([b44d17a](https://github.com/JMRMEDEV/refraict/commit/b44d17ab9390875205f7a813d02bb90ae1b40be9))
* **crop:** add bounded overview+grid crop strategy ([759bd10](https://github.com/JMRMEDEV/refraict/commit/759bd107b4f8c9b9e331bbd132ac789e56c892d5))
* **detect:** add container padding and sibling-gap spacing (Milestone G) ([bf18e51](https://github.com/JMRMEDEV/refraict/commit/bf18e51102dda031a544c6a44628516cd8359465))
* **detect:** add corner-style detection (rounded vs square) ([1f34651](https://github.com/JMRMEDEV/refraict/commit/1f346517a233f201281a35335fc98838ea07b69d))
* **detect:** add semantic text-pattern hints ([339c6c7](https://github.com/JMRMEDEV/refraict/commit/339c6c7e487f8d43176cb9c89d56d496d851d5e3))
* **detect:** bold/font-weight detection ([71692b2](https://github.com/JMRMEDEV/refraict/commit/71692b2d0c18a3734c73db60bc97a38525a7c1ba))
* **detect:** cross-check overview read vs measured evidence ([38f2196](https://github.com/JMRMEDEV/refraict/commit/38f2196b4e76106de80d72c70999d97f02d3543d))
* **detect:** deterministic components, CV region detection, and grounding guard ([eb07801](https://github.com/JMRMEDEV/refraict/commit/eb07801b717adaa92d9cc223ddcf4cd48c11d88e))
* **detect:** deterministic visual-element typing (icon/logo) — Gap 6 Tier 1 ([50018ec](https://github.com/JMRMEDEV/refraict/commit/50018ecc16d471ea25b49ba06839d20292e4b822))
* **detect:** dual-pass CLAHE to recover faint cards on light UIs ([7a02d37](https://github.com/JMRMEDEV/refraict/commit/7a02d37f9b0ed4de1dbd0efe14ec189f14f98314))
* **detect:** surface icons and add Tier-2 grounded VLM element labeling — Gap 6 ([23aa95a](https://github.com/JMRMEDEV/refraict/commit/23aa95a1b3962015ec4d7f56f6a3f9893ce40c85))
* **detect:** typography hierarchy tiering ([4e257aa](https://github.com/JMRMEDEV/refraict/commit/4e257aaf883a6f206b5b2ca91fa00a0147fe28d0))
* **graph:** add repeating-structure detection ([d3dfd6a](https://github.com/JMRMEDEV/refraict/commit/d3dfd6a01c6e7b0ec6eb2c2b4c620a2ff1d25fd3))
* **graph:** additive layout hierarchy (J) ([dc437bf](https://github.com/JMRMEDEV/refraict/commit/dc437bf76bb2ff9140d851e43686ec73937571e4))
* **graph:** associate section headers with repeated groups ([c7f6a6a](https://github.com/JMRMEDEV/refraict/commit/c7f6a6aea3a8e337bae0c1f9709e4207875ec1ed))
* **iconlabel:** Lucide TF-IDF alias map + voting canonicalizer ([c4b7585](https://github.com/JMRMEDEV/refraict/commit/c4b7585a29638513a5ca106e2e7bc77523e7173a))
* **icons:** add 'icons' subcommand and fix element crop framing ([a5db7d0](https://github.com/JMRMEDEV/refraict/commit/a5db7d0f390820dc95a9d914acf6c4d42828f595))
* **icons:** make element-crop padding tunable; default to tighter 0.15 ([c5160b9](https://github.com/JMRMEDEV/refraict/commit/c5160b9c5dbbcb3bb617f0ff85e3fca59c528d16))
* **mcp:** add in-process MCP server with analyze, inspect, get_artifact ([6ac535f](https://github.com/JMRMEDEV/refraict/commit/6ac535feb396173683e2da680af49e2dbc65c047))
* **mcp:** selective query tools ([f980310](https://github.com/JMRMEDEV/refraict/commit/f9803104c120acc0c121e256da2fc083d6aa2700))
* **mcp:** slim analyze summary to rollups ([aafe91c](https://github.com/JMRMEDEV/refraict/commit/aafe91c6debaa388da84d6a2cc135d4c23a27c9f))
* **mcp:** surface corner_styles in the analyze summary ([8599912](https://github.com/JMRMEDEV/refraict/commit/8599912b23bebfc0c02cce1f71484a6508b0ade3))
* **model:** accept grounded markdown output and configurable keep_alive ([6df46f3](https://github.com/JMRMEDEV/refraict/commit/6df46f3e3fc79b769b648b56fad804ab4559c334))
* **ocr:** in-process Tesseract via gosseract as default OCR engine ([dea1e4c](https://github.com/JMRMEDEV/refraict/commit/dea1e4c120f390c11782aea894bfd2fd4b1c361d))
* **pipeline:** gemma self-consolidation, remove qwen from default ([7a02060](https://github.com/JMRMEDEV/refraict/commit/7a02060f7bd0bdf73bab577e60af97560b31e3f2))
* **presets:** add hardware-based config presets ([8bf90fe](https://github.com/JMRMEDEV/refraict/commit/8bf90fe327f343d65ceae50914f3ed86571fae65))
* **vision:** default to gemma3:4b; guard ignores hex codes in numeric check ([c13ebae](https://github.com/JMRMEDEV/refraict/commit/c13ebae73a2248ee7038db7e136ea1ad85490f68))


### Bug Fixes

* **mcp:** keep models warm and use default crop strategy in analyze ([62789e8](https://github.com/JMRMEDEV/refraict/commit/62789e88af87a88dd4be35cf8e86aa574039927a))
* **pipeline:** harden text calls, page-type grounding, chart gate ([5e4e5b4](https://github.com/JMRMEDEV/refraict/commit/5e4e5b4b7b24f6736d41f035857703601419accd))
