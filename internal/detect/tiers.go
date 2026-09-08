package detect

import (
	"sort"

	"github.com/refraict/refraict/internal/ir"
)

// Typography-hierarchy tiering (Milestone H).
//
// Small local VLMs cannot reliably report font size, and Tesseract exposes no
// font metrics — but a glyph's rendered HEIGHT is a robust proxy for relative
// font size on a screenshot. This module clusters the measured text-component
// heights on a page into up to three size bands (heading / body / caption) via a
// deterministic 1-D k-means, then labels each text component with its band.
//
// Per the locked design (docs/roadmap: 2026-09-06 font-size tiering POC), this
// is a POST-OCR analysis on token bboxes refraict already has — it does NOT
// change the pipeline OCR mode and adds no model call. Clustering works on
// per-component heights regardless of OCR line grouping.

// tierNames maps a cluster rank (0 = largest) to a coarse tier label. With k=1
// everything is body; with k=2 the larger band is a heading; with k=3 we also
// split out a caption band below body.
var tierNames3 = []string{"heading", "body", "caption"}

// TierOptions tunes typography tiering.
type TierOptions struct {
	// MinComponents is the fewest text components required before tiering runs.
	// Below this the sample is too small to cluster meaningfully.
	MinComponents int
	// MinBandSeparation is the smallest fractional gap between adjacent band
	// centers (relative to the larger center) for them to count as distinct
	// tiers. Bands closer than this are collapsed (a page in one uniform size
	// stays all-body rather than inventing a spurious heading).
	MinBandSeparation float64
	// MaxIterations bounds the k-means refinement loop.
	MaxIterations int
	// MinHeightFrac excludes degenerate tiny-height text tokens from tiering:
	// any candidate whose text height is below this fraction of the page's
	// MEDIAN text height is skipped (left untiered) rather than clustered. Such
	// tokens are OCR mis-boxes (e.g. a 3px "see" on a page whose body is ~11px)
	// that would otherwise pollute the caption band's floor and make the
	// feature noisy. They remain as text components; only their tier is omitted.
	// 0 disables the filter.
	MinHeightFrac float64
}

// DefaultTierOptions returns sensible defaults tuned on the POC dataset.
func DefaultTierOptions() TierOptions {
	return TierOptions{
		MinComponents:     4,
		MinBandSeparation: 0.18,
		MaxIterations:     50,
		// 0.35 of the median: on the stress set this drops the 2–3px OCR
		// mis-boxes (median ~11px) while keeping legitimate 7–9px small text.
		MinHeightFrac: 0.35,
	}
}

// AttachTextTiers classifies the text components in comps into typographic tiers
// (heading/body/caption) by clustering their heights, and attaches an ir.TextTier
// to each. It only touches components of type "text" that carry text and skips
// those already tiered. Returns the number of components tiered.
//
// It is deterministic (no model) and safe to call unconditionally: with too few
// text components, or when all heights collapse into one band, every text
// component is labeled "body" at level 0.
func AttachTextTiers(comps []ir.Component, opts TierOptions) int {
	// Pass 1: collect eligible text components and their heights.
	var idx []int
	var heights []float64
	for i := range comps {
		c := &comps[i]
		if c.Tier != nil {
			continue
		}
		if c.Type.Value != "text" || c.Text == nil {
			continue
		}
		h := c.BBox.Height()
		if h <= 0 {
			continue
		}
		idx = append(idx, i)
		heights = append(heights, float64(h))
	}
	if len(idx) == 0 {
		return 0
	}

	// Pass 2: drop degenerate tiny-height tokens (OCR mis-boxes) so they do not
	// pollute the caption band. A token is degenerate when its height is below
	// MinHeightFrac * median height across candidates. Filtered tokens are left
	// untiered (they remain text components for grounding/recovery).
	if opts.MinHeightFrac > 0 && len(heights) >= 2 {
		med := medianOf(heights)
		minH := opts.MinHeightFrac * med
		keptIdx := idx[:0:0]
		keptHeights := heights[:0:0]
		for j := range idx {
			if heights[j] < minH {
				continue
			}
			keptIdx = append(keptIdx, idx[j])
			keptHeights = append(keptHeights, heights[j])
		}
		// Only apply the filter if it leaves enough to still cluster; otherwise
		// keep everything (degenerate-heavy pages fall back to tiering all).
		if len(keptIdx) >= 2 {
			idx, heights = keptIdx, keptHeights
		}
	}

	centers, assign := clusterHeights(heights, opts)
	// Rank cluster centers largest-first; map cluster id -> rank (0 = largest).
	order := make([]int, len(centers))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return centers[order[a]] > centers[order[b]] })
	rankOf := make([]int, len(centers))
	for rank, cid := range order {
		rankOf[cid] = rank
	}

	k := len(centers)
	for j, ci := range idx {
		rank := rankOf[assign[j]]
		comps[ci].Tier = &ir.TextTier{
			Tier:       tierLabel(rank, k),
			Level:      rank,
			HeightPx:   int(heights[j] + 0.5),
			Confidence: bandConfidence(heights[j], centers, assign[j]),
		}
	}
	return len(idx)
}

// tierLabel maps a rank within a k-band clustering to a coarse tier name.
//   - k==1: everything is body.
//   - k==2: rank 0 (largest) -> heading, rank 1 -> body.
//   - k>=3: rank 0 -> heading, middle ranks -> body, last rank -> caption.
func tierLabel(rank, k int) string {
	switch {
	case k <= 1:
		return "body"
	case k == 2:
		if rank == 0 {
			return "heading"
		}
		return "body"
	default:
		if rank == 0 {
			return tierNames3[0]
		}
		if rank == k-1 {
			return tierNames3[2]
		}
		return tierNames3[1]
	}
}

// clusterHeights runs a deterministic 1-D k-means over the heights, trying k=3
// then k=2 then k=1, and returns the first clustering whose adjacent band
// centers are separated by at least opts.MinBandSeparation. This prevents
// inventing tiers on a page rendered in a single uniform size. Returns the
// per-cluster centers and, for each input height, its cluster id.
func clusterHeights(heights []float64, opts TierOptions) (centers []float64, assign []int) {
	// Distinct sorted values bound the achievable k.
	distinct := distinctSorted(heights)
	maxK := 3
	if len(distinct) < maxK {
		maxK = len(distinct)
	}
	if maxK < 1 {
		maxK = 1
	}
	for k := maxK; k >= 2; k-- {
		c, a := kmeans1D(heights, k, opts.MaxIterations)
		if bandsSeparated(c, opts.MinBandSeparation) {
			return c, a
		}
	}
	// Fall back to a single band (all body).
	mean := 0.0
	for _, h := range heights {
		mean += h
	}
	mean /= float64(len(heights))
	assign = make([]int, len(heights))
	return []float64{mean}, assign
}

// kmeans1D clusters values into k groups. Seeds centers at evenly-spaced
// quantiles of the sorted distinct values for determinism (no randomness), then
// iterates assign/update to convergence or maxIter. Returns final centers and
// per-value assignments (indices into centers).
func kmeans1D(values []float64, k, maxIter int) (centers []float64, assign []int) {
	if k < 1 {
		k = 1
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	centers = make([]float64, k)
	for i := 0; i < k; i++ {
		// quantile seed at (i+0.5)/k
		q := (float64(i) + 0.5) / float64(k)
		pos := int(q * float64(len(sorted)-1))
		centers[i] = sorted[pos]
	}
	assign = make([]int, len(values))
	for iter := 0; iter < maxIter; iter++ {
		changed := false
		for i, v := range values {
			best, bestD := 0, absf(v-centers[0])
			for c := 1; c < k; c++ {
				if d := absf(v - centers[c]); d < bestD {
					best, bestD = c, d
				}
			}
			if assign[i] != best {
				assign[i] = best
				changed = true
			}
		}
		// Update centers to the mean of assigned points.
		sums := make([]float64, k)
		counts := make([]int, k)
		for i, v := range values {
			sums[assign[i]] += v
			counts[assign[i]]++
		}
		for c := 0; c < k; c++ {
			if counts[c] > 0 {
				centers[c] = sums[c] / float64(counts[c])
			}
		}
		if !changed {
			break
		}
	}
	return centers, assign
}

// bandsSeparated reports whether every adjacent pair of band centers (sorted)
// differs by at least minSep as a fraction of the larger center, and that no
// band is empty of separation (guards against two centers collapsing onto the
// same value).
func bandsSeparated(centers []float64, minSep float64) bool {
	if len(centers) < 2 {
		return false
	}
	sorted := append([]float64(nil), centers...)
	sort.Float64s(sorted)
	for i := 1; i < len(sorted); i++ {
		hi := sorted[i]
		if hi <= 0 {
			return false
		}
		if (hi-sorted[i-1])/hi < minSep {
			return false
		}
	}
	return true
}

// bandConfidence scores how cleanly a value sits in its assigned band: 1 when it
// is much closer to its own center than to the nearest other center, approaching
// 0 at the midpoint between two bands. Single-band clusterings return 1.
func bandConfidence(v float64, centers []float64, own int) float64 {
	if len(centers) < 2 {
		return 1
	}
	ownD := absf(v - centers[own])
	nearestOther := -1.0
	for c := range centers {
		if c == own {
			continue
		}
		d := absf(v - centers[c])
		if nearestOther < 0 || d < nearestOther {
			nearestOther = d
		}
	}
	if nearestOther <= 0 {
		return 0
	}
	// margin: (other - own) / (other + own) in [ -1, 1 ]; clamp to [0,1].
	m := (nearestOther - ownD) / (nearestOther + ownD)
	if m < 0 {
		m = 0
	}
	if m > 1 {
		m = 1
	}
	return m
}

// medianOf returns the median of vs. Non-destructive; returns 0 when empty.
func medianOf(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vs...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func distinctSorted(vs []float64) []float64 {
	sorted := append([]float64(nil), vs...)
	sort.Float64s(sorted)
	var out []float64
	var last float64
	for i, v := range sorted {
		if i == 0 || v != last {
			out = append(out, v)
			last = v
		}
	}
	return out
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
