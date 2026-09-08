// Command collagemeasure is a THROWAWAY POC for the common-frame bold detector.
// Given an image and a set of equal-width cells (a collage of words rendered at
// matched x-height), it measures per-cell ink metrics from PIXELS ONLY (no
// model) and reports which cell reads "heaviest". The hypothesis: on a common
// frame (one rasterization, one threshold), bold text has a measurably higher
// stroke-width / ink concentration than regular text, with a clear margin.
//
// Metrics per cell (ink = the minority dark/light class, auto-polarity):
//   density   : ink pixels / (ink bbox area)   — crude, confounded by glyph mix
//   strokeW   : median horizontal ink-run length (px) — stroke thickness proxy
//   swPerH    : strokeW / ink-bbox height       — size-normalized thickness
//   inkPerCol : mean ink pixels per occupied column — vertical stroke coverage
//
// Usage: collagemeasure -cols N <image>   (splits width into N equal cells)
package main

import (
	"flag"
	"fmt"
	"image"
	"os"
	"sort"

	"github.com/refraict/refraict/internal/imageproc"
	"golang.org/x/image/draw"
)

// measureCellScaled extracts [x0,y0,x1,y1], optionally upscales it, then measures.
func measureCellScaled(im *imageproc.Image, x0, y0, x1, y1, up int) cellMetrics {
	if up <= 1 {
		return measureCell(im, x0, y0, x1, y1)
	}
	sub := im.CropRegion(x0, y0, x1, y1, 0)
	if sub == nil {
		return cellMetrics{}
	}
	sb := sub.Bounds()
	scaled := image.NewRGBA(image.Rect(0, 0, sb.Dx()*up, sb.Dy()*up))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), sub, sb, draw.Over, nil)
	wrapped := imageproc.NewImage(scaled)
	nb := scaled.Bounds()
	return measureCell(wrapped, 0, 0, nb.Dx(), nb.Dy())
}

func main() {
	cols := flag.Int("cols", 2, "number of equal-width cells to split the image into")
	crop := flag.String("crop", "", "measure a single region x0,y0,x1,y1 instead of splitting into cells")
	up := flag.Int("up", 1, "upscale factor for the -crop region before measuring")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: collagemeasure [-cols N | -crop x0,y0,x1,y1] <image>")
		os.Exit(2)
	}
	im, err := imageproc.Load(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b := im.RGBA().Bounds()
	w, h := b.Dx(), b.Dy()

	if *crop != "" {
		var x0, y0, x1, y1 int
		if _, err := fmt.Sscanf(*crop, "%d,%d,%d,%d", &x0, &y0, &x1, &y1); err != nil {
			fmt.Fprintln(os.Stderr, "bad -crop:", err)
			os.Exit(2)
		}
		m := measureCellScaled(im, x0, y0, x1, y1, *up)
		fmt.Printf("%-8s %8s %8s %8s %10s\n", "region", "density", "strokeW", "swPerH", "inkPerCol")
		fmt.Printf("%-8s %8.4f %8.2f %8.4f %10.3f\n", "crop", m.density, m.strokeW, m.swPerH, m.inkPerCol)
		return
	}

	cellW := w / *cols
	fmt.Printf("%-5s %8s %8s %8s %10s\n", "cell", "density", "strokeW", "swPerH", "inkPerCol")
	for c := 0; c < *cols; c++ {
		x0 := c * cellW
		x1 := x0 + cellW
		if c == *cols-1 {
			x1 = w
		}
		m := measureCell(im, x0, 0, x1, h)
		fmt.Printf("%-5d %8.4f %8.2f %8.4f %10.3f\n", c, m.density, m.strokeW, m.swPerH, m.inkPerCol)
	}
}

type cellMetrics struct {
	density, strokeW, swPerH, inkPerCol float64
}

func measureCell(im *imageproc.Image, x0, y0, x1, y1 int) cellMetrics {
	w, h := x1-x0, y1-y0
	lum := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := im.At(x0+x, y0+y)
			lum[y*w+x] = 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
		}
	}
	thr := otsu(lum)
	var below int
	for _, L := range lum {
		if L < thr {
			below++
		}
	}
	inkIsDark := below <= len(lum)/2
	ink := make([]bool, w*h)
	var inkCount int
	for i, L := range lum {
		if (L < thr) == inkIsDark {
			ink[i] = true
			inkCount++
		}
	}
	// Tight ink bbox to normalize out surrounding whitespace.
	minX, minY, maxX, maxY := w, h, -1, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if ink[y*w+x] {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < minX || maxY < minY {
		return cellMetrics{}
	}
	bw, bh := maxX-minX+1, maxY-minY+1
	density := float64(inkCount) / float64(bw*bh)

	// Horizontal ink-run lengths within the ink bbox.
	var runs []float64
	occCols := map[int]int{} // occupied column -> ink pixel count
	for y := minY; y <= maxY; y++ {
		run := 0
		for x := minX; x <= maxX; x++ {
			if ink[y*w+x] {
				run++
				occCols[x]++
			} else if run > 0 {
				runs = append(runs, float64(run))
				run = 0
			}
		}
		if run > 0 {
			runs = append(runs, float64(run))
		}
	}
	strokeW := median(runs)
	var inkColSum int
	for _, n := range occCols {
		inkColSum += n
	}
	inkPerCol := 0.0
	if len(occCols) > 0 {
		inkPerCol = float64(inkColSum) / float64(len(occCols))
	}
	return cellMetrics{
		density:   density,
		strokeW:   strokeW,
		swPerH:    strokeW / float64(bh),
		inkPerCol: inkPerCol / float64(bh), // normalize by glyph height
	}
}

func otsu(lum []float64) float64 {
	const bins = 64
	var hist [bins]int
	for _, L := range lum {
		bi := int(L / 256.0 * bins)
		if bi >= bins {
			bi = bins - 1
		}
		if bi < 0 {
			bi = 0
		}
		hist[bi]++
	}
	total := len(lum)
	var sumAll float64
	for i := 0; i < bins; i++ {
		sumAll += float64(i) * float64(hist[i])
	}
	var sumB, wB, maxVar, thr float64
	for i := 0; i < bins; i++ {
		wB += float64(hist[i])
		if wB == 0 {
			continue
		}
		wF := float64(total) - wB
		if wF == 0 {
			break
		}
		sumB += float64(i) * float64(hist[i])
		mB := sumB / wB
		mF := (sumAll - sumB) / wF
		v := wB * wF * (mB - mF) * (mB - mF)
		if v > maxVar {
			maxVar = v
			thr = float64(i) / bins * 256.0
		}
	}
	return thr
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
