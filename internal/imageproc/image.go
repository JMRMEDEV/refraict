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
