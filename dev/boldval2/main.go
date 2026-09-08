//go:build opencv

// Command boldval2 is the FINAL bold-detection POC. It reuses refraict's real
// infrastructure end to end:
//
//   OCR  →  detect.RegionComponentsOpenCV (types icon/logo/chart/image/text)
//        +  detect.TextComponentsFromOCR
//        →  dub.Reconcile (dedupe by overlap; OCR tokens landing on an icon
//           region get merged into the graphic component)
//        →  bold-check ONLY components typed "text"  (icons/logos excluded, per
//           the plan: refraict already identifies them, so omit from the check)
//
// The bold check itself is the validated recipe: distance-transform SWT with an
// AA-aware grayscale unsharp before Otsu, classified against the page's own
// regular-body baseline (self-calibrating). Prints per-token rows + a summary
// and how many icon/logo/graphic tokens were EXCLUDED.
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

	"github.com/refraict/refraict/internal/detect"
	"github.com/refraict/refraict/internal/dub"
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
	heavyFactor = 1.45
	uncBand     = 0.12
)

// isWordy reports whether a token looks like real text: at least 2 letters and
// at least 55% of its non-space characters are letters. Rejects symbol soup that
// OCR hallucinates from inline icons/glyphs ("@", "£63", "(C3)", "@®").
func isWordy(s string) bool {
	letters, nonspace := 0, 0
	for _, r := range s {
		if r == ' ' {
			continue
		}
		nonspace++
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			letters++
		}
	}
	if letters < 2 || nonspace == 0 {
		return false
	}
	return float64(letters)/float64(nonspace) >= 0.55
}

func isGraphicType(t string) bool {
	switch t {
	case "icon", "logo", "chart", "image":
		return true
	}
	return false
}

func main() {
	perTok := flag.Bool("tokens", false, "print every text token")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: boldval2 [-tokens] <image-or-dir>...")
		os.Exit(2)
	}
	var images []string
	for _, arg := range flag.Args() {
		fi, err := os.Stat(arg)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			es, _ := os.ReadDir(arg)
			for _, e := range es {
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

func runPage(path string, perTok bool) {
	im, err := imageproc.Load(path)
	if err != nil {
		return
	}
	toks, err := ocr.NewTesseractEngine().Recognize(context.Background(), ocr.Input{ImagePath: path})
	if err != nil {
		return
	}

	// Reproduce the real pipeline's typed components.
	regionComps := detectRegion(im.AsImage(), toks)
	textComps := detect.TextComponentsFromOCR(toks, detect.DefaultTextComponentOptions())
	raw := append(append([]ir.Component{}, textComps...), regionComps...)
	merged := dub.Reconcile(raw, dub.Options{IoUThreshold: 0.65, ConfidenceMerge: 0.5})

	// Bold-check candidates: TEXT components only. Icons/logos/charts excluded.
	var cand []ir.Component
	excluded := 0
	for _, c := range merged {
		if isGraphicType(c.Type.Value) {
			excluded++
			continue
		}
		if c.Type.Value != "text" || c.Text == nil {
			continue
		}
		if c.BBox.Height() < minH || c.BBox.Width() < 6 {
			continue
		}
		// Hybrid junk filter: on top of the built-in icon/logo type exclusion,
		// drop tokens OCR hallucinated from small inline glyphs (avatars, gear/
		// folder icons) that the region detector didn't type as graphic. A real
		// word has >=2 letters AND is majority-letters; symbol soup ("@", "£63",
		// "(C3)", "@®") fails the letter ratio.
		if !isWordy(c.Text.Value) {
			excluded++
			continue
		}
		cand = append(cand, c)
	}
	if len(cand) < 5 {
		fmt.Printf("%-28s (too few text comps: %d; excluded %d graphic)\n", filepath.Base(path), len(cand), excluded)
		return
	}

	type m struct {
		text   string
		h      int
		stroke float64
	}
	var ms []m
	var heights []float64
	for _, c := range cand {
		sw, xh := swt(subImage(im, c.BBox, upscale))
		if sw <= 0 || xh <= 0 {
			continue
		}
		ms = append(ms, m{c.Text.Value, c.BBox.Height(), sw})
		heights = append(heights, float64(c.BBox.Height()))
	}
	medH := median(heights)
	var body []float64
	for _, x := range ms {
		if float64(x.h) <= 1.3*medH {
			body = append(body, x.stroke)
		}
	}
	base := median(body)
	heavyLine := base * heavyFactor
	uncLine := heavyLine * (1 - uncBand)

	var nH, nU int
	sort.Slice(ms, func(i, j int) bool { return ms[i].stroke > ms[j].stroke })
	sanityCeiling := base * 3.0 // stroke > 3x baseline is physically impossible for text -> icon contamination
	for _, x := range ms {
		if x.stroke > sanityCeiling {
			excluded++
			continue
		}
		cls := "regular"
		if x.stroke >= heavyLine {
			cls, nH = "HEAVY", nH+1
		} else if x.stroke >= uncLine {
			cls, nU = "uncertain", nU+1
		}
		if perTok && cls != "regular" {
			fmt.Printf("  %-22s h=%-3d stroke=%6.2f -> %s\n", trunc(x.text, 22), x.h, x.stroke, cls)
		}
	}
	fmt.Printf("%-28s text=%-3d excludedGraphic=%-2d base=%.2f heavyLine=%.2f  HEAVY=%d uncertain=%d\n",
		filepath.Base(path), len(ms), excluded, base, heavyLine, nH, nU)
}

func detectRegion(img image.Image, toks []ir.OCRToken) []ir.Component {
	comps, err := detect.RegionComponentsOpenCV(img, detect.DefaultOpenCVRegionOptions(), toks)
	if err != nil {
		return nil
	}
	return comps
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
func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
