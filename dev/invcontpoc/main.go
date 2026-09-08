//go:build opencv

// Command invcontpoc is a THROWAWAY POC for INVISIBLE-container inference:
// grouping detected boxes that share an alignment band into an implied
// column/row container that has NO border (flex/whitespace grouping the CV
// detector can't see). Method: cluster boxes by shared x-band (overlapping
// horizontal extent + regular vertical stacking => a COLUMN) and by shared
// y-band (=> a ROW). Emit the bounding box of each cluster as an inferred
// container, gated by: >=minMembers, members roughly the same cross-axis width,
// and low overlap (they stack, not pile). Prints inferred columns/rows per page.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/refraict/refraict/internal/detect"
	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
)

func main() {
	minMembers := flag.Int("min", 2, "min members to infer a container")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: invcontpoc [-min N] <image-or-dir>...")
		os.Exit(2)
	}
	var images []string
	for _, a := range flag.Args() {
		fi, err := os.Stat(a)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			es, _ := os.ReadDir(a)
			for _, e := range es {
				if isImg(e.Name()) {
					images = append(images, filepath.Join(a, e.Name()))
				}
			}
		} else if isImg(a) {
			images = append(images, a)
		}
	}
	sort.Strings(images)
	for _, p := range images {
		runPage(p, *minMembers)
	}
}

func isImg(n string) bool {
	n = strings.ToLower(n)
	return strings.HasSuffix(n, ".png") || strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".jpeg")
}

func runPage(path string, minMembers int) {
	im, err := imageproc.Load(path)
	if err != nil {
		return
	}
	opts := detect.DefaultOpenCVRegionOptions()
	boxes, err := detect.DetectRegionsOpenCVRawPOC(im.AsImage(), opts)
	if err != nil || len(boxes) == 0 {
		return
	}
	// Use only card/panel-ish boxes (not tiny icon contours) as layout members.
	var bs []ir.BoundingBox
	for _, b := range boxes {
		if b.BBox.Width() >= 40 && b.BBox.Height() >= 24 {
			bs = append(bs, b.BBox)
		}
	}
	if len(bs) < minMembers {
		return
	}

	cols := inferColumns(bs, minMembers)
	rows := inferRows(bs, minMembers)
	if len(cols) == 0 && len(rows) == 0 {
		return
	}
	fmt.Printf("== %s ==  boxes=%d\n", filepath.Base(path), len(bs))
	for _, c := range cols {
		fmt.Printf("  COLUMN  %dx%d at (%d,%d)  members=%d  (stacked, shared x-band)\n",
			c.box.Width(), c.box.Height(), c.box.X0, c.box.Y0, c.members)
	}
	for _, r := range rows {
		fmt.Printf("  ROW     %dx%d at (%d,%d)  members=%d  (side-by-side, shared y-band)\n",
			r.box.Width(), r.box.Height(), r.box.X0, r.box.Y0, r.members)
	}
}

type container struct {
	box     ir.BoundingBox
	members int
}

// inferColumns clusters boxes whose horizontal extents mostly overlap (same
// x-band) and that stack vertically without overlapping each other.
func inferColumns(bs []ir.BoundingBox, minMembers int) []container {
	used := make([]bool, len(bs))
	var out []container
	for i := range bs {
		if used[i] {
			continue
		}
		cluster := []int{i}
		for j := range bs {
			if j == i || used[j] {
				continue
			}
			if xOverlapFrac(bs[i], bs[j]) >= 0.6 && sameWidth(bs[i], bs[j], 0.35) {
				cluster = append(cluster, j)
			}
		}
		if len(cluster) < minMembers {
			continue
		}
		// Require vertical stacking (low mutual vertical overlap).
		if !stacked(bs, cluster, true) {
			continue
		}
		for _, k := range cluster {
			used[k] = true
		}
		out = append(out, container{box: bound(bs, cluster), members: len(cluster)})
	}
	return out
}

// inferRows clusters boxes sharing a y-band that sit side-by-side.
func inferRows(bs []ir.BoundingBox, minMembers int) []container {
	used := make([]bool, len(bs))
	var out []container
	for i := range bs {
		if used[i] {
			continue
		}
		cluster := []int{i}
		for j := range bs {
			if j == i || used[j] {
				continue
			}
			if yOverlapFrac(bs[i], bs[j]) >= 0.6 && sameHeight(bs[i], bs[j], 0.35) {
				cluster = append(cluster, j)
			}
		}
		if len(cluster) < minMembers {
			continue
		}
		if !stacked(bs, cluster, false) {
			continue
		}
		for _, k := range cluster {
			used[k] = true
		}
		out = append(out, container{box: bound(bs, cluster), members: len(cluster)})
	}
	return out
}

func xOverlapFrac(a, b ir.BoundingBox) float64 {
	o := minI(a.X1, b.X1) - maxI(a.X0, b.X0)
	if o <= 0 {
		return 0
	}
	m := minI(a.Width(), b.Width())
	if m <= 0 {
		return 0
	}
	return float64(o) / float64(m)
}
func yOverlapFrac(a, b ir.BoundingBox) float64 {
	o := minI(a.Y1, b.Y1) - maxI(a.Y0, b.Y0)
	if o <= 0 {
		return 0
	}
	m := minI(a.Height(), b.Height())
	if m <= 0 {
		return 0
	}
	return float64(o) / float64(m)
}
func sameWidth(a, b ir.BoundingBox, tol float64) bool {
	wa, wb := float64(a.Width()), float64(b.Width())
	if wa == 0 || wb == 0 {
		return false
	}
	return absF(wa-wb)/maxF(wa, wb) <= tol
}
func sameHeight(a, b ir.BoundingBox, tol float64) bool {
	ha, hb := float64(a.Height()), float64(b.Height())
	if ha == 0 || hb == 0 {
		return false
	}
	return absF(ha-hb)/maxF(ha, hb) <= tol
}

// stacked reports whether cluster members are separated along the given axis
// (vertical=true => stacked top-to-bottom) with low mutual overlap.
func stacked(bs []ir.BoundingBox, cluster []int, vertical bool) bool {
	overlaps := 0
	pairs := 0
	for a := 0; a < len(cluster); a++ {
		for b := a + 1; b < len(cluster); b++ {
			pairs++
			if vertical {
				if yOverlapFrac(bs[cluster[a]], bs[cluster[b]]) > 0.3 {
					overlaps++
				}
			} else {
				if xOverlapFrac(bs[cluster[a]], bs[cluster[b]]) > 0.3 {
					overlaps++
				}
			}
		}
	}
	if pairs == 0 {
		return false
	}
	return float64(overlaps)/float64(pairs) < 0.25
}

func bound(bs []ir.BoundingBox, cluster []int) ir.BoundingBox {
	out := bs[cluster[0]]
	for _, k := range cluster[1:] {
		b := bs[k]
		if b.X0 < out.X0 {
			out.X0 = b.X0
		}
		if b.Y0 < out.Y0 {
			out.Y0 = b.Y0
		}
		if b.X1 > out.X1 {
			out.X1 = b.X1
		}
		if b.Y1 > out.Y1 {
			out.Y1 = b.Y1
		}
	}
	return out
}

func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
