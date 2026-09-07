package ocr

import (
	"context"
	"image"
	"image/color"
	"os"
	"strconv"

	"github.com/otiai10/gosseract/v2"
	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
	"golang.org/x/image/draw"
)

// TesseractEngine runs OCR in-process via libtesseract (CGo, gosseract),
// mirroring the reference Python adapter's deterministic UI-screenshot prep:
//
//  1. Auto-invert dark-background images (Tesseract expects dark-on-light).
//  2. Upscale ~2x so small UI text clears Tesseract's minimum legible height.
//
// Word boxes are mapped back to ORIGINAL image coordinates (the upscale factor
// is divided out). This is the default OCR path; REFRAICT_OCR_CMD still overrides
// it with an external command when set. No subprocess, no Python/Pillow.
type TesseractEngine struct {
	Scale         float64 // upscale factor (default 2.0)
	DarkThreshold float64 // mean luminance below which we invert (default 110)
	MinConf       float64 // drop tokens below this confidence 0..1 (default 0)
}

// NewTesseractEngine returns a TesseractEngine with defaults matching the Python
// adapter, overridable via the same REFRAICT_OCR_* env vars for parity.
func NewTesseractEngine() *TesseractEngine {
	e := &TesseractEngine{Scale: 2.0, DarkThreshold: 110, MinConf: 0}
	if v := os.Getenv("REFRAICT_OCR_SCALE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			e.Scale = f
		}
	}
	if v := os.Getenv("REFRAICT_OCR_DARK_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			e.DarkThreshold = f
		}
	}
	if v := os.Getenv("REFRAICT_OCR_MIN_CONF"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			e.MinConf = f
		}
	}
	return e
}

// Recognize runs the in-process Tesseract pipeline.
func (e *TesseractEngine) Recognize(ctx context.Context, in Input) ([]ir.OCRToken, error) {
	src, err := loadImage(in)
	if err != nil {
		return nil, err
	}
	scale := e.Scale
	if scale <= 0 {
		scale = 1
	}
	prepped := preprocess(src, scale, e.DarkThreshold)

	pngBytes, err := imageproc.EncodePNG(prepped)
	if err != nil {
		return nil, err
	}

	client := gosseract.NewClient()
	defer client.Close()
	if err := client.SetImageFromBytes(pngBytes); err != nil {
		return nil, err
	}
	boxes, err := client.GetBoundingBoxes(gosseract.RIL_WORD)
	if err != nil {
		return nil, err
	}

	toks := make([]ir.OCRToken, 0, len(boxes))
	for _, b := range boxes {
		text := normalizeToken(b.Word)
		if text == "" {
			continue
		}
		conf := b.Confidence / 100.0 // gosseract reports 0..100
		if conf < e.MinConf {
			continue
		}
		// Map box back to original image coordinates (divide out the upscale).
		x0 := int(float64(b.Box.Min.X) / scale)
		y0 := int(float64(b.Box.Min.Y) / scale)
		x1 := int(float64(b.Box.Max.X) / scale)
		y1 := int(float64(b.Box.Max.Y) / scale)
		toks = append(toks, ir.OCRToken{
			Text:       text,
			BBoxGlobal: ir.BoundingBox{X0: x0, Y0: y0, X1: x1, Y1: y1},
			Confidence: conf,
			Source:     "tesseract",
		})
	}
	return toks, nil
}

var _ Engine = (*TesseractEngine)(nil)

// loadImage reads the input image (path or bytes) as an image.Image.
func loadImage(in Input) (image.Image, error) {
	if in.ImagePath != "" {
		im, err := imageproc.Load(in.ImagePath)
		if err != nil {
			return nil, err
		}
		return im.AsImage(), nil
	}
	im, _, err := imageproc.Decode(in.Data)
	if err != nil {
		return nil, err
	}
	return im, nil
}

// preprocess auto-inverts dark-background images and upscales, matching the
// reference Python adapter.
func preprocess(src image.Image, scale, darkThreshold float64) image.Image {
	work := src
	if meanLuminance(src) < darkThreshold {
		work = invert(src)
	}
	if scale != 1.0 {
		b := work.Bounds()
		nw := int(float64(b.Dx()) * scale)
		nh := int(float64(b.Dy()) * scale)
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.CatmullRom.Scale(dst, dst.Bounds(), work, b, draw.Over, nil)
		return dst
	}
	return work
}

func meanLuminance(img image.Image) float64 {
	b := img.Bounds()
	// coarse grid sample (matches the Python 64x64 downsample spirit)
	stepX := b.Dx() / 64
	stepY := b.Dy() / 64
	if stepX < 1 {
		stepX = 1
	}
	if stepY < 1 {
		stepY = 1
	}
	var total float64
	var n int
	for y := b.Min.Y; y < b.Max.Y; y += stepY {
		for x := b.Min.X; x < b.Max.X; x += stepX {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			total += 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B)
			n++
		}
	}
	if n == 0 {
		return 255
	}
	return total / float64(n)
}

func invert(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			dst.SetRGBA(x-b.Min.X, y-b.Min.Y, color.RGBA{255 - c.R, 255 - c.G, 255 - c.B, 255})
		}
	}
	return dst
}

// normalizeToken applies the same tiny whole-token OCR-artifact allowlist the
// Python adapter used (USD misreads), preserving trailing punctuation.
func normalizeToken(text string) string {
	core := text
	trail := ""
	for len(core) > 0 {
		last := core[len(core)-1]
		if last == '.' || last == ',' || last == ':' || last == ';' || last == ')' {
			trail = string(last) + trail
			core = core[:len(core)-1]
		} else {
			break
		}
	}
	switch lower(core) {
	case "usp", "uso", "usd":
		return "USD" + trail
	}
	return text
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}
