// Package imageproc provides deterministic image ingest and processing:
// loading, hashing, high-quality resizing, row/column projection for
// segmentation hints, and pixel color sampling.
package imageproc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"sort"
	"strings"

	"golang.org/x/image/draw"
)

// Image wraps a decoded image with its bytes and hash.
type Image struct {
	img      image.Image
	Bytes    []byte
	Sha256   string
	Path     string
	Original image.Image
}

// Load decodes an image file (PNG, JPEG) and computes its SHA-256 hash.
// The original decoded image is retained for coordinate matching.
func Load(path string) (*Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read image %s: %w", path, err)
	}
	img, original, err := Decode(data)
	if err != nil {
		return nil, fmt.Errorf("decode image %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return &Image{
		img:      img,
		Bytes:    data,
		Sha256:   hex.EncodeToString(sum[:]),
		Path:     path,
		Original: original,
	}, nil
}

// Decode decodes image bytes. Returns a normalized RGBA image and the original
// decoded image (for detecting the source format).
func Decode(data []byte) (image.Image, image.Image, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	_ = cfg
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	// Normalize to RGBA for uniform sampling.
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	_ = format
	return rgba, img, nil
}


// NewImage wraps an arbitrary image.Image as a normalized RGBA *Image with no
// original file backing. Useful for tests and for images generated in-memory.
func NewImage(src image.Image) *Image {
	b := src.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	return &Image{img: rgba, Original: src}
}

// Bounds returns the image bounds (width, height).
func (im *Image) Bounds() (int, int) {
	b := im.img.Bounds()
	return b.Dx(), b.Dy()
}

// AsImage returns the underlying image.Image for use with generic image APIs.
func (im *Image) AsImage() image.Image { return im.img }

// RGBA returns the normalized RGBA image.
func (im *Image) RGBA() *image.RGBA {
	if r, ok := im.img.(*image.RGBA); ok {
		return r
	}
	// Normalize.
	b := im.img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), im.img, b.Min, draw.Src)
	return rgba
}

// At returns an RGBA color at global pixel coordinates.
func (im *Image) At(x, y int) color.RGBA {
	if r, ok := im.img.(*image.RGBA); ok {
		c := r.RGBAAt(x, y)
		return c
	}
	r, g, b, a := im.img.At(x, y).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

// Resize returns a new image scaled to fit within maxLong, preserving aspect
// ratio, using a high-quality Lanczos filter. If the image is already within
// the bound it is returned unchanged.
func (im *Image) Resize(maxLong int) image.Image {
	dstW, dstH := FitWithin(im.img.Bounds().Dx(), im.img.Bounds().Dy(), maxLong)
	if dstW == im.img.Bounds().Dx() && dstH == im.img.Bounds().Dy() {
		return im.img
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), im.img, im.img.Bounds(), draw.Over, nil)
	return dst
}

// CropRegion extracts the rectangle [x0,y0,x1,y1] from the image, clamped to
// the image bounds. If maxLong > 0 the extracted region is scaled so its longest
// side does not exceed maxLong. Returns a new image; the caller owns it.
func (im *Image) CropRegion(x0, y0, x1, y1, maxLong int) image.Image {
	ib := im.img.Bounds()
	if x0 < ib.Min.X {
		x0 = ib.Min.X
	}
	if y0 < ib.Min.Y {
		y0 = ib.Min.Y
	}
	if x1 > ib.Max.X {
		x1 = ib.Max.X
	}
	if y1 > ib.Max.Y {
		y1 = ib.Max.Y
	}
	if x1 <= x0 || y1 <= y0 {
		return nil
	}
	sub := image.NewRGBA(image.Rect(0, 0, x1-x0, y1-y0))
	draw.Draw(sub, sub.Bounds(), im.img, image.Pt(x0, y0), draw.Src)
	if maxLong > 0 {
		sw, sh := FitWithin(sub.Bounds().Dx(), sub.Bounds().Dy(), maxLong)
		if sw != sub.Bounds().Dx() || sh != sub.Bounds().Dy() {
			dst := image.NewRGBA(image.Rect(0, 0, sw, sh))
			draw.CatmullRom.Scale(dst, dst.Bounds(), sub, sub.Bounds(), draw.Over, nil)
			return dst
		}
	}
	return sub
}

// HasBarChartGeometry reports whether the region [x0,y0,x1,y1] of img exhibits a
// bar-chart-like signal: several vertical columns of high foreground ink,
// separated by low-ink gaps, sharing an approximate common baseline. It is a
// deterministic gate used to reject a VLM's free-text "bar chart"/"chart" label
// on graphics that are not actually charts (icons, logos, tiles) — a common
// small-VLM failure mode. Conservative by design: it returns true only when the
// bar signal is clear (>= minBars regular bars), so it rejects blobs/glyphs and
// accepts genuine bar charts.
//
// Method: build a per-column foreground-ink projection over the region relative
// to the region's dominant (background) color, threshold it into "ink columns",
// group contiguous ink columns into bars separated by clear gaps, and require at
// least minBars such bars with a shared bottom baseline.
func HasBarChartGeometry(img image.Image, x0, y0, x1, y1 int) bool {
	const minBars = 3
	if x1-x0 < 12 || y1-y0 < 24 {
		return false
	}
	w := x1 - x0
	h := y1 - y0
	// Reject wide, short bands (button rows, text lines): a bar chart is not
	// dramatically wider than it is tall. Text rows produce many uniform-height
	// ink columns that a naive projection mistakes for bars.
	if w > h*6 {
		return false
	}
	// Dominant color of the region approximates the background.
	bgHex, _, _, _, ok := SampleRegion(img, x0, y0, x1, y1, 0.0)
	if !ok {
		return false
	}
	bg, err := HexToRGB(bgHex)
	if err != nil {
		return false
	}
	// Foreground mask: a pixel is "ink" if it differs enough from background.
	const inkThresh = 48 // per-channel Manhattan distance floor
	colInk := make([]int, w)         // ink pixel count per column
	colMaxY := make([]int, w)        // lowest ink row per column (baseline probe)
	for i := range colMaxY {
		colMaxY[i] = -1
	}
	for cx := 0; cx < w; cx++ {
		for cy := 0; cy < h; cy++ {
			c := color.RGBAModel.Convert(img.At(x0+cx, y0+cy)).(color.RGBA)
			d := abs(int(c.R)-bg[0]) + abs(int(c.G)-bg[1]) + abs(int(c.B)-bg[2])
			if d >= inkThresh {
				colInk[cx]++
				if cy > colMaxY[cx] {
					colMaxY[cx] = cy
				}
			}
		}
	}
	// A column is "filled" if its ink height is a meaningful fraction of the
	// region height (bars are tall vertical strokes, not stray pixels).
	minColInk := h / 4
	if minColInk < 3 {
		minColInk = 3
	}
	// Group contiguous filled columns into bars, requiring a clear gap between.
	bars := 0
	baselines := []int{}
	barHeights := []int{}
	inBar := false
	barMaxBase := -1
	barMaxInk := 0
	for cx := 0; cx < w; cx++ {
		filled := colInk[cx] >= minColInk
		if filled {
			if colMaxY[cx] > barMaxBase {
				barMaxBase = colMaxY[cx]
			}
			if colInk[cx] > barMaxInk {
				barMaxInk = colInk[cx]
			}
			inBar = true
		} else {
			if inBar {
				bars++
				baselines = append(baselines, barMaxBase)
				barHeights = append(barHeights, barMaxInk)
				barMaxBase = -1
				barMaxInk = 0
			}
			inBar = false
		}
	}
	if inBar {
		bars++
		baselines = append(baselines, barMaxBase)
		barHeights = append(barHeights, barMaxInk)
	}
	if bars < minBars {
		return false
	}
	// Require bar-height VARIANCE. A real bar chart has bars of differing
	// heights; a row of text or a set of equal UI ticks has near-uniform ink
	// heights. Reject when the spread is negligible relative to the tallest bar.
	maxH, minH := 0, 1<<30
	for _, bh := range barHeights {
		if bh > maxH {
			maxH = bh
		}
		if bh < minH {
			minH = bh
		}
	}
	if maxH <= 0 || (maxH-minH)*100 < maxH*20 { // require >=20% height spread
		return false
	}
	// Require a shared baseline: bar bottoms cluster near the region bottom.
	base := 0
	for _, b := range baselines {
		base += b
	}
	base /= len(baselines)
	near := 0
	tol := h / 8
	if tol < 4 {
		tol = 4
	}
	for _, b := range baselines {
		if abs(b-base) <= tol {
			near++
		}
	}
	// Most bars must share the baseline.
	return near*2 >= len(baselines)*2-1 && near >= minBars
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// FitWithin computes target width/height for an image with the given source
// dimensions so that the longest side does not exceed maxLong.
func FitWithin(srcW, srcH, maxLong int) (int, int) {
	long := srcW
	if srcH > long {
		long = srcH
	}
	if long <= maxLong {
		return srcW, srcH
	}
	scale := float64(maxLong) / float64(long)
	return int(float64(srcW) * scale), int(float64(srcH) * scale)
}

// PadBox expands a box [x0,y0,x1,y1] by frac of its size on each side, clamped
// to the given image dimensions (w,h). Used to give a small VLM visual context
// around a tiny element before cropping.
func PadBox(x0, y0, x1, y1, w, h int, frac float64) (int, int, int, int) {
	padX := int(float64(x1-x0) * frac)
	padY := int(float64(y1-y0) * frac)
	nx0, ny0, nx1, ny1 := x0-padX, y0-padY, x1+padX, y1+padY
	if nx0 < 0 {
		nx0 = 0
	}
	if ny0 < 0 {
		ny0 = 0
	}
	if nx1 > w {
		nx1 = w
	}
	if ny1 > h {
		ny1 = h
	}
	return nx0, ny0, nx1, ny1
}

// ElementCropPNG renders a graphic element for a small VLM: it crops the region
// [x0,y0,x1,y1], resizes so the longest side fits within `inner`, and CENTERS
// it on a square `canvas`×`canvas` image filled with bg (so padding blends
// rather than adding a hard edge). Consistent centered framing measured better
// for small-VLM icon recognition than a raw variable-size crop. Returns PNG
// bytes, or nil on empty/degenerate input.
func (im *Image) ElementCropPNG(x0, y0, x1, y1, canvas, inner int, bg color.RGBA) []byte {
	if x1 <= x0 || y1 <= y0 || canvas <= 0 || inner <= 0 {
		return nil
	}
	// Extract the raw crop at native resolution (no cap).
	sub := im.CropRegion(x0, y0, x1, y1, 0)
	if sub == nil {
		return nil
	}
	sb := sub.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 {
		return nil
	}
	// Scale the crop so its LONGEST side == inner (upscale small icons; the
	// prior cap-only path left tiny icons as specks on the canvas). Preserve
	// aspect ratio.
	long := sw
	if sh > long {
		long = sh
	}
	scale := float64(inner) / float64(long)
	nw, nh := int(float64(sw)*scale), int(float64(sh)*scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	scaled := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), sub, sb, draw.Over, nil)

	dst := image.NewRGBA(image.Rect(0, 0, canvas, canvas))
	for y := 0; y < canvas; y++ {
		for x := 0; x < canvas; x++ {
			dst.SetRGBA(x, y, bg)
		}
	}
	offX := (canvas - nw) / 2
	offY := (canvas - nh) / 2
	draw.Draw(dst, image.Rect(offX, offY, offX+nw, offY+nh), scaled, scaled.Bounds().Min, draw.Src)
	data, err := EncodePNG(dst)
	if err != nil {
		return nil
	}
	return data
}

// EncodePNG encodes an image as PNG.
func EncodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeJPEG encodes an image as JPEG.
func EncodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WritePNG writes an image to a file.
func WritePNG(path string, img image.Image) error {
	data, err := EncodePNG(img)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// SampleRegion computes the median color of interior pixels in a region.
// borderPct is the fraction removed from each edge to avoid border/text
// contamination. Returns hex string and r,g,b.
func SampleRegion(img image.Image, x0, y0, x1, y1 int, borderPct float64) (hex string, r, g, b int, ok bool) {
	w := x1 - x0
	h := y1 - y0
	if w <= 0 || h <= 0 {
		return "", 0, 0, 0, false
	}
	padX := int(float64(w) * borderPct)
	padY := int(float64(h) * borderPct)
	ix0 := x0 + padX
	iy0 := y0 + padY
	ix1 := x1 - padX
	iy1 := y1 - padY
	if ix1 <= ix0 || iy1 <= iy0 {
		ix0, iy0, ix1, iy1 = x0, y0, x1, y1
	}
	var rs, gs, bs []int
	for y := iy0; y < iy1; y++ {
		for x := ix0; x < ix1; x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			if c.A < 128 {
				continue
			}
			rs = append(rs, int(c.R))
			gs = append(gs, int(c.G))
			bs = append(bs, int(c.B))
		}
	}
	if len(rs) == 0 {
		return "", 0, 0, 0, false
	}
	sort.Ints(rs)
	sort.Ints(gs)
	sort.Ints(bs)
	r = rs[len(rs)/2]
	g = gs[len(gs)/2]
	b = bs[len(bs)/2]
	return fmt.Sprintf("#%02X%02X%02X", r, g, b), r, g, b, true
}

// HexToRGB converts a hex color to RGB.
func HexToRGB(hex string) ([3]int, error) {
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		return [3]int{}, fmt.Errorf("invalid hex color %q", hex)
	}
	var rgb [3]int
	for i := 0; i < 3; i++ {
		_, err := fmt.Sscanf(h[i*2:i*2+2], "%02X", &rgb[i])
		if err != nil {
			return rgb, err
		}
	}
	return rgb, nil
}

// DetectFormat returns the file format for the image.
func (im *Image) DetectFormat() string {
	if len(im.Bytes) > 3 && im.Bytes[0] == 0xFF && im.Bytes[1] == 0xD8 {
		return "jpeg"
	}
	if len(im.Bytes) > 4 && im.Bytes[0] == 0x89 && im.Bytes[1] == 0x50 && im.Bytes[2] == 0x4E {
		return "png"
	}
	return "unknown"
}
