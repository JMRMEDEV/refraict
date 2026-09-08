//go:build opencv

// Command swtregpoc is a THROWAWAY POC for the SELF-CALIBRATING bold detector.
//
// Insight from prior POCs: (a) distance-transform SWT gives a robust ABSOLUTE
// stroke width, but (b) matching it to synthetic references fails on calibration
// (the UI's real font weight differs from any fixed reference pool), and (c)
// normalizing by height (sw/xh) inverts on real UIs because weight & size
// covary.
//
// This variant sidesteps all three: on a page, MOST text is regular, so the
// relationship stroke-width ~ height is dominated by regular text. Fit a robust
// line to (height, strokeW) across ALL tokens; that line approximates "what a
// REGULAR token of this height measures" — in the UI's OWN font (self-calibrating,
// no references). Bold tokens sit ABOVE the line (positive residual). Flag heavy
// when the residual is a strong positive outlier (robust-z on residuals). No
// synthetic fonts, no model, no sw/xh inversion.
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
	k := flag.Float64("k", 2.0, "robust-z on POSITIVE residual to call heavy")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: swtregpoc [flags] <image>")
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

	type tk struct {
		text   string
		xh, sw float64
	}
	var ts []tk
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
		ts = append(ts, tk{t.Text, xh, sw})
	}
	if len(ts) < 5 {
		fmt.Println("too few tokens")
		return
	}

	// Robust line fit strokeW = a + m*xh via Theil–Sen (median of pairwise
	// slopes) — resistant to the bold outliers we're trying to find.
	xs := make([]float64, len(ts))
	ys := make([]float64, len(ts))
	for i, t := range ts {
		xs[i], ys[i] = t.xh, t.sw
	}
	a, m := theilSen(xs, ys)

	// Residuals; robust spread via MAD of residuals.
	res := make([]float64, len(ts))
	for i, t := range ts {
		res[i] = t.sw - (a + m*t.xh)
	}
	medRes := median(res)
	mad := medianAbsDev(res, medRes)
	if mad <= 0 {
		mad = 0.5
	}

	fmt.Printf("== %s ==  fit sw=%.2f+%.4f*xh  MAD(res)=%.2f (K=%.1f)\n", path, a, m, mad, *k)
	fmt.Printf("  %-22s %6s %7s %8s %7s %s\n", "text", "xh", "sw", "expReg", "z+", "class")
	type outrow struct {
		text            string
		xh, sw, exp, z  float64
	}
	var rows []outrow
	for _, t := range ts {
		exp := a + m*t.xh
		z := (t.sw - exp) / (1.4826 * mad)
		rows = append(rows, outrow{t.text, t.xh, t.sw, exp, z})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].z > rows[j].z })
	for _, r := range rows {
		cls := "regular"
		switch {
		case r.z >= *k:
			cls = "HEAVY"
		case r.z >= *k-0.75:
			cls = "uncertain"
		}
		txt := r.text
		if len(txt) > 22 {
			txt = txt[:22]
		}
		fmt.Printf("  %-22s %6.0f %7.2f %8.2f %7.2f %s\n", txt, r.xh, r.sw, r.exp, r.z, cls)
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

// theilSen returns (intercept, slope) via the median of pairwise slopes.
func theilSen(xs, ys []float64) (a, m float64) {
	var slopes []float64
	for i := 0; i < len(xs); i++ {
		for j := i + 1; j < len(xs); j++ {
			if xs[j] != xs[i] {
				slopes = append(slopes, (ys[j]-ys[i])/(xs[j]-xs[i]))
			}
		}
	}
	if len(slopes) == 0 {
		return median(ys), 0
	}
	m = median(slopes)
	// intercept = median(y - m*x)
	inter := make([]float64, len(xs))
	for i := range xs {
		inter[i] = ys[i] - m*xs[i]
	}
	a = median(inter)
	return a, m
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
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
