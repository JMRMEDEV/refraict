// Command regcheck is a DEV TOOL (not part of the shipped binary). It runs the
// region detector on an image, prints the detected boxes, and writes an overlay
// PNG with the boxes drawn, for visual inspection against ground truth.
//
// Usage:
//
//	go run ./dev/regcheck <image> [overlay-out.png]              # pure-Go detector
//	go run -tags opencv ./dev/regcheck <image> [overlay-out.png] # OpenCV detector
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: regcheck <image> [overlay-out.png]")
		os.Exit(1)
	}
	in := os.Args[1]
	out := "overlay.png"
	if len(os.Args) >= 3 {
		out = os.Args[2]
	}
	f, err := os.Open(in)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		panic(err)
	}

	comps := detectComponents(img)
	fmt.Printf("detected %d regions\n", len(comps))
	for _, c := range comps {
		b := c.BBox
		fmt.Printf("  %-6s %-9s bbox=[%d,%d,%d,%d] %dx%d conf=%.2f\n",
			c.ID, c.Type.Value, b.X0, b.Y0, b.X1, b.Y1, b.Width(), b.Height(), c.Confidence)
	}

	// Draw overlay.
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
	col := color.RGBA{255, 0, 128, 255}
	for _, c := range comps {
		drawBox(rgba, c.BBox.X0, c.BBox.Y0, c.BBox.X1, c.BBox.Y1, col)
	}
	of, err := os.Create(out)
	if err != nil {
		panic(err)
	}
	defer of.Close()
	if err := png.Encode(of, rgba); err != nil {
		panic(err)
	}
	fmt.Printf("overlay written to %s\n", out)
}

func drawBox(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	th := 2
	for t := 0; t < th; t++ {
		for x := x0; x < x1; x++ {
			img.Set(x, y0+t, c)
			img.Set(x, y1-1-t, c)
		}
		for y := y0; y < y1; y++ {
			img.Set(x0+t, y, c)
			img.Set(x1-1-t, y, c)
		}
	}
}
