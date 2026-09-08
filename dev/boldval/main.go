//go:build opencv

// Command boldval is the consolidated validation harness for the bold detector.
// It combines the three techniques that each proved out in the POC series:
//
//  1. Distance-transform SWT for a robust absolute stroke width (caps/tracking-
//     safe).
//  2. AA-aware grayscale unsharp BEFORE Otsu — thins anti-aliased regular strokes
//     more than solid bold cores, widening the bold/regular gap at screenshot res.
//  3. Self-calibration to the PAGE'S OWN regular-body baseline (median stroke of
//     body-tier tokens) — no synthetic reference font, so no calibration mismatch.
//
// Classification (per token): heavy if stroke >= baseline * heavyFactor;
// uncertain in a band just below; else regular. Reports per-token rows and a
// per-page summary; runs across a directory of images.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/ocr"
	"gocv.io/x/gocv"
	"golang.org/x/image/draw"
)

var (
	sharpen     = 3.0
	upscale     = 6
	minConf     = 0.6
	minH        = 10
	heavyFactor = 1.45 // stroke >= baseline*heavyFactor => heavy
	uncBand     = 0.12 // within this fraction below the heavy line => uncertain
)

func main() {
	perTok := flag.Bool("tokens", false, "print every token (else per-page summary only)")
	hf := flag.Float64("factor", heavyFactor, "heavy threshold = baseline * factor")
	sh := flag.Float64("sharpen", sharpen, "AA unsharp amount")
	flag.Parse()
	heavyFactor = *hf
	sharpen = *sh
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: boldval [-tokens] [-factor F] [-sharpen S] <image-or-dir>...")
		os.Exit(2)
	}
	var images []string
	for _, arg := range flag.Args() {
		fi, err := os.Stat(arg)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			entries, _ := os.ReadDir(arg)
			for _, e := range entries {
				if isImg(e.Name()) {
					images = append(images, filepath.Join(arg, e.Name()))
				}
			}
		} else if isImg(arg) {
			images = append(images, arg)
		}
	}
	sort.Strings(images)
	for _, p := range images {
		runPage(p, *perTok)
	}
}

func isImg(n string) bool {
	n = strings.ToLower(n)
	return strings.HasSuffix(n, ".png") || strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".jpeg")
}

type tokM struct {
	text   string
	h      int
	stroke float64
	xh     float64
}

func runPage(path string, perTok bool) {
	im, err := imageproc.Load(path)
	if err != nil {
		return
	}
	toks, err := ocr.NewTesseractEngine().Recognize(context.Background(), ocr.Input{ImagePath: path})
	if err != nil {
		return
	}
	var ms []tokM
	var heights []float64
	for _, t := range toks {
		if t.Confidence > 0 && t.Confidence < minConf {
			continue
		}
		b := t.BBoxGlobal
		if b.Height() < minH || b.Width() < 6 {
			continue
		}
		// skip non-word tokens (icons/symbols OCR'd as junk) — require >=2 letters
		if letters(t.Text) < 2 {
			continue
		}
		sw, xh := swt(subImage(im, b, upscale))
		if sw <= 0 || xh <= 0 {
			continue
		}
		ms = append(ms, tokM{t.Text, b.Height(), sw, xh})
		heights = append(heights, float64(b.Height()))
	}
	if len(ms) < 5 {
		fmt.Printf("%-28s (too few tokens)\n", filepath.Base(path))
		return
	}
	// Body baseline: median stroke over body-tier tokens (height <= 1.3*medianH).
	medH := median(heights)
	var bodyStroke []float64
	for _, m := range ms {
		if float64(m.h) <= 1.3*medH {
			bodyStroke = append(bodyStroke, m.stroke)
		}
	}
	base := median(bodyStroke)
	heavyLine := base * heavyFactor
	uncLine := heavyLine * (1 - uncBand)

	var nHeavy, nUnc int
	sort.Slice(ms, func(i, j int) bool { return ms[i].stroke > ms[j].stroke })
	for _, m := range ms {
		cls := classify(m.stroke, heavyLine, uncLine)
		if cls == "HEAVY" {
			nHeavy++
		} else if cls == "uncertain" {
			nUnc++
		}
		if perTok {
			fmt.Printf("  %-22s h=%-3d stroke=%6.2f base=%.2f -> %s\n", trunc(m.text, 22), m.h, m.stroke, base, cls)
		}
	}
	fmt.Printf("%-28s tokens=%-3d base=%.2f heavyLine=%.2f  HEAVY=%d uncertain=%d\n",
		filepath.Base(path), len(ms), base, heavyLine, nHeavy, nUnc)
}

func classify(s, heavyLine, uncLine float64) string {
	switch {
	case s >= heavyLine:
		return "HEAVY"
	case s >= uncLine:
		return "uncertain"
	default:
		return "regular"
	}
}

func letters(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			n++
		}
	}
	return n
}
func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
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

func swt(img image.Image) (strokeW, xheight float64) {
	mat, err := gocv.ImageToMatRGB(img)
	if err != nil || mat.Empty() {
		return 0, 0
	}
	defer mat.Close()
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(mat, &gray, gocv.ColorBGRToGray)
	if sharpen > 0 {
		blur := gocv.NewMat()
		gocv.GaussianBlur(gray, &blur, image.Pt(0, 0), 3.0, 3.0, gocv.BorderDefault)
		sharp := gocv.NewMat()
		gocv.AddWeighted(gray, 1.0+sharpen, blur, -sharpen, 0, &sharp)
		blur.Close()
		gray.Close()
		gray = sharp
	}
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
