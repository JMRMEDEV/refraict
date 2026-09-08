//go:build opencv

// Command swtpoc is a THROWAWAY POC: measure stroke width via an OpenCV distance
// transform (SWT-style), which is robust to caps/tracking/size unlike the
// run-length ÷ height proxy. For a crop it binarizes (Otsu), distance-transforms
// the ink, and estimates stroke width = 2 * (robust ridge distance). Reports an
// ABSOLUTE stroke width in px AND one normalized by x-height, so we can see
// which is stable across real vs synthetic renderings.
//
// Modes:
//   swtpoc -crop x0,y0,x1,y1 [-up N] <image>   measure one token region
//   swtpoc -cols N <image>                     measure N equal cells (collage)
package main

import (
	"flag"
	"fmt"
	"image"
	"os"
	"sort"

	"github.com/refraict/refraict/internal/imageproc"
	"gocv.io/x/gocv"
	"golang.org/x/image/draw"
)

func main() {
	crop := flag.String("crop", "", "region x0,y0,x1,y1")
	cols := flag.Int("cols", 0, "split width into N equal cells")
	up := flag.Int("up", 1, "upscale factor before measuring")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: swtpoc [-crop x0,y0,x1,y1 | -cols N] [-up N] <image>")
		os.Exit(2)
	}
	im, err := imageproc.Load(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b := im.RGBA().Bounds()

	fmt.Printf("%-10s %8s %8s %10s\n", "region", "strokeW", "xheight", "sw/xh")
	if *crop != "" {
		var x0, y0, x1, y1 int
		fmt.Sscanf(*crop, "%d,%d,%d,%d", &x0, &y0, &x1, &y1)
		report("crop", subImage(im, x0, y0, x1, y1, *up))
		return
	}
	n := *cols
	if n <= 0 {
		n = 1
	}
	cw := b.Dx() / n
	for c := 0; c < n; c++ {
		x0 := c * cw
		x1 := x0 + cw
		if c == n-1 {
			x1 = b.Dx()
		}
		report(fmt.Sprintf("cell%d", c), subImage(im, x0, 0, x1, b.Dy(), *up))
	}
}

func report(name string, img image.Image) {
	sw, xh := swtStrokeWidth(img)
	ratio := 0.0
	if xh > 0 {
		ratio = sw / xh
	}
	fmt.Printf("%-10s %8.2f %8.2f %10.4f\n", name, sw, xh, ratio)
}

func subImage(im *imageproc.Image, x0, y0, x1, y1, up int) image.Image {
	sub := im.CropRegion(x0, y0, x1, y1, 0)
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

func sharpenAmount() float64 {
	if v := os.Getenv("SHARPEN"); v != "" {
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err == nil {
			return f
		}
	}
	return 0
}

// swtStrokeWidth returns (stroke width px, x-height px) using an OpenCV distance
// transform. Stroke width = 2 * mean(DT values >= 80th percentile of nonzero DT)
// — the ridge running down the middle of each stroke has DT ~ half stroke width.
// x-height is the ink-bbox height (rows containing ink), used for normalization.
func swtStrokeWidth(img image.Image) (strokeW, xheight float64) {
	mat, err := gocv.ImageToMatRGB(img)
	if err != nil || mat.Empty() {
		return 0, 0
	}
	defer mat.Close()
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(mat, &gray, gocv.ColorBGRToGray)

	// AA-aware enhancement (env SHARPEN=amount, e.g. 1.5): unsharp mask on the
	// GRAYSCALE before thresholding, to crisp the anti-aliased stroke edges so
	// Otsu recovers a truer stroke boundary (bold vs regular gap widened rather
	// than collapsed by a single hard cutoff). unsharp: g + a*(g - blur(g)).
	if amt := sharpenAmount(); amt > 0 {
		blur := gocv.NewMat()
		gocv.GaussianBlur(gray, &blur, image.Pt(0, 0), 3.0, 3.0, gocv.BorderDefault)
		sharp := gocv.NewMat()
		gocv.AddWeighted(gray, 1.0+amt, blur, -amt, 0, &sharp)
		blur.Close()
		gray.Close()
		gray = sharp
	}

	// Otsu binarize. Ink must be white (255) for DistanceTransform (distance to
	// zero/background). Auto-polarity: if the mean is dark (dark theme), the
	// ink is light -> normal binary; else invert so text becomes white.
	bin := gocv.NewMat()
	defer bin.Close()
	gocv.Threshold(gray, &bin, 0, 255, gocv.ThresholdBinary+gocv.ThresholdOtsu)
	// Ensure ink (minority class) is white: count white; if > half, invert.
	white := gocv.CountNonZero(bin)
	total := bin.Rows() * bin.Cols()
	if white > total/2 {
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

	// Collect nonzero DT values; find ink-bbox rows for x-height.
	var vals []float64
	minRow, maxRow := bin.Rows(), -1
	for y := 0; y < dt.Rows(); y++ {
		rowHasInk := false
		for x := 0; x < dt.Cols(); x++ {
			v := float64(dt.GetFloatAt(y, x))
			if v > 0 {
				vals = append(vals, v)
				rowHasInk = true
			}
		}
		if rowHasInk {
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
	// Ridge estimate: mean of top 20% of DT values.
	start := int(0.80 * float64(len(vals)))
	if start >= len(vals) {
		start = len(vals) - 1
	}
	var sum float64
	for _, v := range vals[start:] {
		sum += v
	}
	ridge := sum / float64(len(vals)-start)
	strokeW = 2 * ridge
	if maxRow >= minRow {
		xheight = float64(maxRow - minRow + 1)
	}
	return strokeW, xheight
}
