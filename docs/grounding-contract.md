# Grounding & cross-check packet contract

**Status:** authoritative (2026-10-23). This pins the JSON types of refraict's
diagnostic grounding signals so downstream consumers (the `ui_analyze` plugin,
pyromon `verify_ui`) can decode them correctly. It exists because a one-character
naming collision between two similar fields (`*_support` vs `*_supported`) caused a
consumer to type a float ratio as an int, which crashed the verify_ui Layer-2 gate
on any real screen (a fractional ratio cannot unmarshal into `int`).

Source of truth: `internal/detect/guard.go` (`GroundingReport`) and
`internal/detect/crosscheck.go` (`CrossCheckReport`).

---

## `evidence/grounding.json` — `GroundingReport` (the summary grounding guard)

Written by `CheckGrounding`. Flags summary claims unsupported by measured evidence.

| JSON key | Go type | Semantics |
|---|---|---|
| `grounded` | `bool` | true iff `unsupported_claims` is empty. |
| `unsupported_claims` | `[]UnsupportedClaim` | each `{kind, claim, reason}`; `kind` ∈ `color`/`behavior`/`text`/`number`. |
| **`color_support`** | **`float64` — RATIO in [0.0, 1.0]** | fraction of summary color mentions that ARE supported by a measured color. **1.0 when no colors are mentioned.** Diagnostic only. |
| **`text_support`** | **`float64` — RATIO in [0.0, 1.0]** | fraction of quoted text spans that ARE supported by the OCR corpus. **1.0 when nothing is quoted.** Diagnostic only. |

> **Decode `color_support` / `text_support` as a JSON number (float64 / `*float64`),
> never as an int.** A real screen typically yields a fraction such as `0.5833`
> (7 of 12 mentions supported). Coercing it to int both fails to parse *and* would
> lie about grounding (0.58 → 0). These two ratios are **diagnostic honesty signals
> only — NOT a MET-gate input** (the measured MET decision is the component-color
> audit, not these ratios).

---

## `evidence/crosscheck.json` — `CrossCheckReport` (the overview cross-check)

Written by `CrossCheck`. A second, independent read (the overview VLM description)
compared to measured evidence.

| JSON key | Go type | Semantics |
|---|---|---|
| `consistent` | `bool` | true iff no unsupported overview claims were found. |
| `agreement` | `float64` — RATIO in [0.0, 1.0] | fraction of checkable overview claims supported (1.0 when none). |
| `unsupported_claims` | `[]UnsupportedClaim` | overview claims not backed by evidence. |
| `text_checked` | `int` — COUNT | number of checkable text claims. |
| **`text_supported`** | **`int` — COUNT** | number of text claims supported. |
| `color_checked` | `int` — COUNT | number of checkable color claims. |
| **`color_supported`** | **`int` — COUNT** | number of color claims supported. |
| `count_checked` | `int` — COUNT | number of checkable count claims. |
| `count_supported` | `int` — COUNT | number of count claims supported. |

---

## ⚠️ The naming collision (do not re-conflate)

Two near-identical names, **different fields, different types, different files**:

| key | file / struct | type | meaning |
|---|---|---|---|
| `color_support` | `guard.go` `GroundingReport.ColorSupport` | **`float64` ratio** | supported / total, in [0,1] |
| `color_supported` | `crosscheck.go` `CrossCheckReport.ColorOK` | **`int` count** | number supported |
| `text_support` | `guard.go` `GroundingReport.TextSupport` | **`float64` ratio** | supported / total, in [0,1] |
| `text_supported` | `crosscheck.go` `CrossCheckReport.TextOK` | **`int` count** | number supported |

Rule of thumb: **`…support` = float ratio; `…supported` = int count.** They differ by
one character. Pin consumer struct types to this table.

---

## Consumer guidance (pyromon `verify_ui` / `ui_analyze`)

The `ui_analyze` packet passes `color_support` / `text_support` through verbatim from
`grounding.json`. A consumer decoding the packet MUST type them `*float64`:

```go
type analyzePacket struct {
    // ...
    ColorSupport *float64 `json:"color_support"` // ratio [0,1], diagnostic
    TextSupport  *float64 `json:"text_support"`  // ratio [0,1], diagnostic
    // ...
}
```

A fractional value (e.g. `0.5833`) is the COMMON case on a real screen; the
whole-number case (`1`) is only the "no mentions" default and must not be used to
infer an `int` type.
