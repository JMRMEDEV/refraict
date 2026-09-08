// Command strokepoc is a THROWAWAY proof-of-concept measuring whether a
// pixel-based stroke-width signal separates bold from regular UI text on real
// screenshots. It is NOT shipped: it lives under dev/ and is not imported by the
// binary or MCP server.
//
// Method (deliberately simple for a POC — no gocv, pure Go):
//  1. OCR the image (in-process Tesseract) to get word tokens + bboxes.
//  2. For each token, crop its bbox, binarize into ink/background by luminance
//     (auto-detecting whether text is dark-on-light or light-on-dark), and
//     measure stroke width via the median length of horizontal ink runs (a
//     cheap Stroke-Width-Transform proxy) plus ink density.
//  3. Normalize stroke width by glyph height (token height) so it is comparable
//     across font sizes, and report per token plus a per-page robust z-score.
//
// We then eyeball the printed table against the screenshot to see whether
// visually-bold tokens sit above regular ones on the normalized stroke metric,
// and by how much — i.e. whether a real milestone is worth building.
package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/color"
	"os"
	"sort"
	"strings"

	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/model"
	"github.com/refraict/refraict/internal/ocr"
	"golang.org/x/image/draw"
)

func main() {
	minConf := flag.Float64("min-conf", 0.5, "drop OCR tokens below this confidence")
	minH := flag.Int("min-h", 8, "drop tokens shorter than this (px) as OCR noise")
	top := flag.Int("top", 0, "if >0, only print the N highest-stroke tokens")
	scale := flag.Int("scale", 4, "upscale each token crop by this factor before measuring")
	sharpen := flag.Float64("sharpen", 0, "unsharp-mask amount applied to luminance before binarizing (0=off)")
	vlm := flag.Bool("vlm", false, "also ask gemma to vote bold/regular per token (needs Ollama)")
	runs := flag.Int("runs", 5, "VLM samples per token for voting")
	vlmMax := flag.Int("vlm-max", 12, "max tokens to send to the VLM (highest-stroke first)")
	endpoint := flag.String("endpoint", "http://localhost:11434", "Ollama endpoint")
	vmodel := flag.String("model", "gemma3:4b", "vision model name")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: strokepoc [flags] <image> [image...]")
		os.Exit(2)
	}
	opts := runOpts{
		minConf: *minConf, minH: *minH, top: *top, scale: *scale, sharpen: *sharpen,
		vlm: *vlm, runs: *runs, vlmMax: *vlmMax, endpoint: *endpoint, vmodel: *vmodel,
	}
	for _, path := range flag.Args() {
		if err := run(path, opts); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
		}
	}
}

type runOpts struct {
	minConf        float64
	minH, top      int
	scale          int
	sharpen        float64
	vlm            bool
	runs, vlmMax   int
	endpoint       string
	vmodel         string
}

type tokRow struct {
	text    string
	h       int
	runlen  float64 // median horizontal ink-run length (px)
	density float64 // ink fraction of bbox
	swNorm  float64 // runlen / height (size-normalized stroke width)
	bbox    ir.BoundingBox
	// VLM vote (only when -vlm): bold/regular/uncertain agreement.
	vlmVerdict   string
	vlmAgree     int
	vlmSamples   int
}

func run(path string, o runOpts) error {
	im, err := imageproc.Load(path)
	if err != nil {
		return err
	}
	eng := ocr.NewTesseractEngine()
	toks, err := eng.Recognize(context.Background(), ocr.Input{ImagePath: path})
	if err != nil {
		return err
	}
	scale := o.scale
	if scale < 1 {
		scale = 1
	}

	var rows []tokRow
	for _, t := range toks {
		if t.Confidence > 0 && t.Confidence < o.minConf {
			continue
		}
		b := t.BBoxGlobal
		if b.Height() < o.minH || b.Width() < 3 {
			continue
		}
		rl, dens, ok := strokeMetrics(im, b, scale, o.sharpen)
		if !ok {
			continue
		}
		rows = append(rows, tokRow{
			text:    t.Text,
			h:       b.Height(),
			runlen:  rl,
			density: dens,
			swNorm:  rl / float64(b.Height()),
			bbox:    b,
		})
	}
	if len(rows) == 0 {
		fmt.Printf("== %s ==\n  (no usable tokens)\n", path)
		return nil
	}

	// Page baseline: median normalized stroke width. Bold candidates are clear
	// upward outliers relative to this.
	sw := make([]float64, len(rows))
	for i, r := range rows {
		sw[i] = r.swNorm
	}
	med := median(sw)
	mad := medianAbsDev(sw, med)

	sort.Slice(rows, func(i, j int) bool { return rows[i].swNorm > rows[j].swNorm })

	// Optional VLM vote on the highest-stroke tokens (bounded, like MaxElementLabels).
	if o.vlm {
		vision := model.NewOllama(o.endpoint, o.vmodel)
		limit := o.vlmMax
		if limit > len(rows) {
			limit = len(rows)
		}
		for i := 0; i < limit; i++ {
			data := textCropBytes(im, rows[i].bbox, scale)
			if len(data) == 0 {
				continue
			}
			v, a, s := voteBold(context.Background(), vision, data, rows[i], o.runs)
			rows[i].vlmVerdict, rows[i].vlmAgree, rows[i].vlmSamples = v, a, s
		}
	}

	fmt.Printf("== %s ==  tokens=%d  swNorm median=%.3f MAD=%.3f\n", path, len(rows), med, mad)
	fmt.Printf("  %-24s %4s %7s %7s %7s %-6s %s\n", "text", "h", "runlen", "swNorm", "z", "detFlag", "vlm(vote)")
	n := len(rows)
	if o.top > 0 && o.top < n {
		n = o.top
	}
	for i := 0; i < n; i++ {
		r := rows[i]
		z := 0.0
		if mad > 0 {
			z = (r.swNorm - med) / (1.4826 * mad) // robust z-score
		}
		detFlag := ""
		if z >= 2.0 {
			detFlag = "BOLD?"
		}
		vlmCol := ""
		if r.vlmSamples > 0 {
			vlmCol = fmt.Sprintf("%s %d/%d", r.vlmVerdict, r.vlmAgree, r.vlmSamples)
		}
		txt := r.text
		if len(txt) > 24 {
			txt = txt[:24]
		}
		fmt.Printf("  %-24s %4d %7.2f %7.3f %7.2f %-6s %s\n", txt, r.h, r.runlen, r.swNorm, z, detFlag, vlmCol)
	}
	return nil
}

// textCropBytes builds an enhanced crop for the VLM: the token bbox on a padded
// canvas, upscaled — the deterministic-enhancement step applied to the image the
// model actually sees. Reuses the icon-crop framing helper.
func textCropBytes(im *imageproc.Image, b ir.BoundingBox, scale int) []byte {
	// Pad ~30% horizontally / 60% vertically so the model sees whole glyphs with
	// a little context, then render onto a 512 canvas (same as element crops).
	padX := (b.X1 - b.X0) * 3 / 10
	padY := (b.Y1 - b.Y0) * 6 / 10
	x0, y0 := b.X0-padX, b.Y0-padY
	x1, y1 := b.X1+padX, b.Y1+padY
	return im.ElementCropPNG(x0, y0, x1, y1, 512, 448, color.RGBA{255, 255, 255, 255})
}

// voteBold asks the VLM N times whether the cropped word is bold, and returns
// the majority verdict (bold|regular|uncertain), its agreement count, and N.
func voteBold(ctx context.Context, vision model.VisionBackend, data []byte, r tokRow, runs int) (verdict string, agree, samples int) {
	if runs <= 0 {
		return "", 0, 0
	}
	prompt := fmt.Sprintf(`This image shows the word %q from a UI screenshot, enlarged.
Is the text rendered in a BOLD (heavy) font weight, or a REGULAR (normal) weight?
Answer with exactly one word: BOLD or REGULAR.`, r.text)
	tally := map[string]int{}
	for i := 0; i < runs; i++ {
		res, err := vision.Analyze(ctx, model.VisionRequest{
			ImageData: data, ImageMIME: "image/png", CropID: "t", Prompt: prompt,
		})
		if err != nil || res == nil {
			continue
		}
		low := strings.ToLower(res.Description)
		switch {
		case strings.Contains(low, "bold"):
			tally["bold"]++
		case strings.Contains(low, "regular") || strings.Contains(low, "normal"):
			tally["regular"]++
		default:
			tally["uncertain"]++
		}
	}
	best, k := "uncertain", 0
	for v, c := range tally {
		if c > k {
			best, k = v, c
		}
	}
	return best, k, runs
}

// strokeMetrics crops the token bbox, binarizes ink vs background, and returns
// (median horizontal ink-run length, ink density, ok). Auto-detects polarity
// (dark text on light bg, or the inverse) from the crop's own luminance
// histogram so it works on dark-theme UIs without the pipeline's global invert.
func strokeMetrics(im *imageproc.Image, b ir.BoundingBox, scale int, sharpen float64) (runlen, density float64, ok bool) {
	crop := im.CropRegion(b.X0, b.Y0, b.X1, b.Y1, 0)
	if crop == nil {
		return 0, 0, false
	}
	if scale > 1 {
		src := crop.Bounds()
		up := image.NewRGBA(image.Rect(0, 0, src.Dx()*scale, src.Dy()*scale))
		draw.CatmullRom.Scale(up, up.Bounds(), crop, src, draw.Over, nil)
		crop = up
	}
	cb := crop.Bounds()
	w, h := cb.Dx(), cb.Dy()
	if w < 3 || h < 3 {
		return 0, 0, false
	}
	// Luminance grid.
	lum := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := crop.At(cb.Min.X+x, cb.Min.Y+y).RGBA()
			L := 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(bl>>8)
			lum[y*w+x] = L
		}
	}
	// Deterministic enhancement: unsharp mask on the luminance channel to crisp
	// stroke edges before binarizing (interpolation alone adds no edge detail).
	if sharpen > 0 {
		lum = unsharp(lum, w, h, sharpen)
	}
	thr := otsu(lum)
	// Decide polarity: ink is the MINORITY class (text covers less area than bg).
	var below int
	for _, L := range lum {
		if L < thr {
			below++
		}
	}
	inkIsDark := below <= len(lum)/2 // fewer dark pixels => dark text on light bg

	ink := make([]bool, w*h)
	var inkCount int
	for i, L := range lum {
		isDark := L < thr
		isInk := isDark == inkIsDark
		ink[i] = isInk
		if isInk {
			inkCount++
		}
	}
	density = float64(inkCount) / float64(w*h)

	// Median horizontal ink-run length across all scanlines (SWT proxy): the
	// typical horizontal thickness of a stroke. Robust-ish to character mix.
	var runs []float64
	for y := 0; y < h; y++ {
		run := 0
		for x := 0; x < w; x++ {
			if ink[y*w+x] {
				run++
			} else if run > 0 {
				runs = append(runs, float64(run))
				run = 0
			}
		}
		if run > 0 {
			runs = append(runs, float64(run))
		}
	}
	if len(runs) == 0 {
		return 0, density, false
	}
	// Divide the measured run length back into ORIGINAL-image pixels so swNorm
	// (runlen/height) stays comparable regardless of the upscale factor.
	return median(runs) / float64(scale), density, true
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
	var sumB, wB float64
	var maxVar, thr float64
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

// unsharp applies a 3x3 box-blur unsharp mask to a luminance grid: out = L +
// amount*(L - blur(L)). Crisps stroke edges before binarization. Clamped 0..255.
func unsharp(lum []float64, w, h int, amount float64) []float64 {
	blur := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sum float64
			var n int
			for dy := -1; dy <= 1; dy++ {
				yy := y + dy
				if yy < 0 || yy >= h {
					continue
				}
				for dx := -1; dx <= 1; dx++ {
					xx := x + dx
					if xx < 0 || xx >= w {
						continue
					}
					sum += lum[yy*w+xx]
					n++
				}
			}
			blur[y*w+x] = sum / float64(n)
		}
	}
	out := make([]float64, w*h)
	for i := range lum {
		v := lum[i] + amount*(lum[i]-blur[i])
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		out[i] = v
	}
	return out
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

func medianAbsDev(xs []float64, med float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	dev := make([]float64, len(xs))
	for i, x := range xs {
		d := x - med
		if d < 0 {
			d = -d
		}
		dev[i] = d
	}
	return median(dev)
}
