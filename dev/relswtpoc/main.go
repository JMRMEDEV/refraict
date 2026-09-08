//go:build opencv

// Command relswtpoc is a THROWAWAY POC for the relative-SWT bold detector.
//
// Combining the two winning ideas from prior POCs:
//   - Distance-transform SWT gives a robust stroke width (handles caps/tracking).
//   - RELATIVE-to-body-baseline neutralizes the absolute font-scale calibration
//     issue that broke same-word synthetic matching.
//
// Pipeline (no synthetic references, no model):
//   1. OCR the page for token text + bboxes.
//   2. For each token, upscale its crop and measure SWT stroke width (sw) and
//      ink x-height (xh). The weight signal is sw/xh (stroke per unit height) —
//      size-invariant.
//   3. Establish the page's REGULAR-BODY baseline: the median sw/xh over "body"
//      tokens (height within the central band; body text is overwhelmingly
//      regular on real UIs), using a robust MAD for spread.
//   4. Classify each token: heavy if its sw/xh exceeds baseline by >= K*MAD
//      (default K=3), i.e. a clear robust-z outlier; else regular; withhold when
//      near the boundary.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"os"
	"sort"

	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/ocr"
	"gocv.io/x/gocv"
	"golang.org/x/image/draw"
)

func main() {
	minConf := flag.Float64("min-conf", 0.6, "drop OCR tokens below this confidence")
	minH := flag.Int("min-h", 10, "drop tokens shorter than this (px)")
	up := flag.Int("up", 6, "upscale factor per token crop")
	k := flag.Float64("k", 3.0, "robust-z threshold above body baseline to call heavy")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: relswtpoc [flags] <image>")
		os.Exit(2)
	}
	path := flag.Arg(0)
	im, err := imageproc.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	toks, err := ocr.NewTesseractEngine().Recognize(context.Background(), ocr.Input{ImagePath: path})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	type row struct {
		text  string
		h     int
		swxh  float64
	}
	var rows []row
	var heights []float64
	for _, t := range toks {
		if t.Confidence > 0 && t.Confidence < *minConf {
			continue
		}
		b := t.BBoxGlobal
		if b.Height() < *minH || b.Width() < 6 {
			continue
		}
		sw, xh := swtMeasure(subImage(im, b, *up))
		if sw <= 0 || xh <= 0 {
			continue
		}
		rows = append(rows, row{t.Text, b.Height(), sw / xh})
		heights = append(heights, float64(b.Height()))
	}
	if len(rows) < 4 {
		fmt.Println("too few tokens")
		return
	}

	// Body baseline: median sw/xh over BODY-height tokens (central height band,
	// 25th–60th pct) — these are overwhelmingly regular. Robust MAD for spread.
	medH := percentile(heights, 0.5)
	var bodySwxh []float64
	for _, r := range rows {
		if float64(r.h) <= 1.3*medH { // body/caption, not big headings
			bodySwxh = append(bodySwxh, r.swxh)
		}
	}
	base := median(bodySwxh)
	mad := medianAbsDev(bodySwxh, base)
	if mad <= 0 {
		mad = 0.01
	}

	fmt.Printf("== %s ==  bodyBaseline sw/xh=%.4f MAD=%.4f (K=%.1f)\n", path, base, mad, *k)
	fmt.Printf("  %-22s %4s %8s %7s %s\n", "text", "h", "sw/xh", "z", "class")
	sort.Slice(rows, func(i, j int) bool { return rows[i].swxh > rows[j].swxh })
	for _, r := range rows {
		z := (r.swxh - base) / (1.4826 * mad)
		cls := "regular"
		switch {
		case z >= *k:
			cls = "HEAVY"
		case z >= *k-1.0:
			cls = "uncertain"
		}
		txt := r.text
		if len(txt) > 22 {
			txt = txt[:22]
		}
		fmt.Printf("  %-22s %4d %8.4f %7.2f %s\n", txt, r.h, r.swxh, z, cls)
	}
}

func subImage(im *imageproc.Image, b ir.BoundingBox, up int) image.Image {
	sub := im.CropRegion(b.X0, b.Y0, b.X1, b.Y1, 0)
	if sub == nil {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	if up <= 1 {
		return sub
	}
	sb := sub.Bounds()
	scaled := image.NewRGBA(image.Rect(0, 0, sb.Dx()*up, sb.Dy()*up))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), sub, sb, draw.Over, nil)
	return scaled
}

func swtMeasure(img image.Image) (strokeW, xheight float64) {
	mat, err := gocv.ImageToMatRGB(img)
	if err != nil || mat.Empty() {
		return 0, 0
	}
	defer mat.Close()
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(mat, &gray, gocv.ColorBGRToGray)
	bin := gocv.NewMat()
	defer bin.Close()
	gocv.Threshold(gray, &bin, 0, 255, gocv.ThresholdBinary+gocv.ThresholdOtsu)
	if gocv.CountNonZero(bin) > bin.Rows()*bin.Cols()/2 {
		inv := gocv.NewMat()
		gocv.BitwiseNot(bin, &inv)
		bin.Close()
		bin = inv
	}
	dt := gocv.NewMat()
	defer dt.Close()
	labels := gocv.NewMat()
	defer labels.Close()
	gocv.DistanceTransform(bin, &dt, &labels, gocv.DistL2, gocv.DistanceMask5, gocv.DistanceLabelCComp)
	var vals []float64
	minRow, maxRow := bin.Rows(), -1
	for y := 0; y < dt.Rows(); y++ {
		has := false
		for x := 0; x < dt.Cols(); x++ {
			if v := float64(dt.GetFloatAt(y, x)); v > 0 {
				vals = append(vals, v)
				has = true
			}
		}
		if has {
			if y < minRow {
				minRow = y
			}
			if y > maxRow {
				maxRow = y
			}
		}
	}
	if len(vals) == 0 {
		return 0, 0
	}
	sort.Float64s(vals)
	start := int(0.80 * float64(len(vals)))
	if start >= len(vals) {
		start = len(vals) - 1
	}
	var sum float64
	for _, v := range vals[start:] {
		sum += v
	}
	strokeW = 2 * sum / float64(len(vals)-start)
	if maxRow >= minRow {
		xheight = float64(maxRow - minRow + 1)
	}
	return strokeW, xheight
}

func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	i := int(p * float64(len(s)-1))
	return s[i]
}
func median(xs []float64) float64 { return percentile(xs, 0.5) }
func medianAbsDev(xs []float64, m float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	d := make([]float64, len(xs))
	for i, x := range xs {
		if x >= m {
			d[i] = x - m
		} else {
			d[i] = m - x
		}
	}
	return median(d)
}
