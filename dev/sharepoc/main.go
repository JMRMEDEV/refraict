//go:build opencv

// Command sharepoc is a THROWAWAY POC for "measured occupancy fraction" — how
// much of a container each direct child takes along the container's dominant
// stacking axis (the flexbox/percentage question, answered as a MEASURED share,
// not a claimed CSS property).
//
// Reuses the real pipeline: OCR -> region detect + text components -> reconcile
// -> graph containment. For each container it finds DIRECT children (not grand-
// children), picks the dominant axis, and reports each child's extent fraction
// of the span covered by the children, plus gaps. A tiling score (how completely
// children fill the parent inner box) gates whether the shares are trustworthy.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/refraict/refraict/internal/detect"
	"github.com/refraict/refraict/internal/dub"
	"github.com/refraict/refraict/internal/graph"
	"github.com/refraict/refraict/internal/imageproc"
	"github.com/refraict/refraict/internal/ir"
	"github.com/refraict/refraict/internal/ocr"
)

func main() {
	minKids := flag.Int("min-children", 2, "only containers with >= this many direct children")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: sharepoc [-min-children N] <image-or-dir>...")
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
		runPage(p, *minKids)
	}
}

func isImg(n string) bool {
	n = strings.ToLower(n)
	return strings.HasSuffix(n, ".png") || strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".jpeg")
}

func runPage(path string, minKids int) {
	im, err := imageproc.Load(path)
	if err != nil {
		return
	}
	toks, _ := ocr.NewTesseractEngine().Recognize(context.Background(), ocr.Input{ImagePath: path})
	region := detectRegion(im, toks)
	text := detect.TextComponentsFromOCR(toks, detect.DefaultTextComponentOptions())
	raw := append(append([]ir.Component{}, text...), region...)
	merged := dub.Reconcile(raw, dub.Options{IoUThreshold: 0.65, ConfidenceMerge: 0.5})
	g := graph.Build(merged)

	idx := map[string]int{}
	for i := range merged {
		idx[merged[i].ID] = i
	}
	contains := map[string][]string{}
	for _, r := range g.Relationships {
		if r.Relation == "contains" {
			contains[r.A] = append(contains[r.A], r.B)
		}
	}
	directChildren := func(pid string) []string {
		kids := contains[pid]
		var direct []string
		for _, b := range kids {
			bi := idx[b]
			grand := false
			for _, c := range kids {
				if c == b {
					continue
				}
				ci := idx[c]
				if merged[ci].BBox.Contains(merged[bi].BBox) && merged[ci].BBox.Area() > merged[bi].BBox.Area() {
					grand = true
					break
				}
			}
			if !grand {
				direct = append(direct, b)
			}
		}
		return direct
	}

	printed := false
	// Deterministic container order.
	var pids []string
	for pid := range contains {
		pids = append(pids, pid)
	}
	sort.Strings(pids)
	for _, pid := range pids {
		kids := directChildren(pid)
		if len(kids) < minKids {
			continue
		}
		p := merged[idx[pid]].BBox
		type kb struct {
			id string
			b  ir.BoundingBox
		}
		var kbs []kb
		minY, maxY, minX, maxX := 1<<30, -(1 << 30), 1<<30, -(1 << 30)
		for _, k := range kids {
			b := merged[idx[k]].BBox
			kbs = append(kbs, kb{k, b})
			if b.Y0 < minY {
				minY = b.Y0
			}
			if b.Y1 > maxY {
				maxY = b.Y1
			}
			if b.X0 < minX {
				minX = b.X0
			}
			if b.X1 > maxX {
				maxX = b.X1
			}
		}
		vertical := (maxY - minY) >= (maxX - minX)
		if !printed {
			fmt.Printf("== %s ==\n", filepath.Base(path))
			printed = true
		}
		axis := "col(V)"
		var parentExtent, span int
		if vertical {
			parentExtent, span = p.Height(), maxY-minY
		} else {
			axis, parentExtent, span = "row(H)", p.Width(), maxX-minX
		}
		tiling := 0.0
		if parentExtent > 0 {
			tiling = float64(span) / float64(parentExtent)
		}
		sort.Slice(kbs, func(i, j int) bool {
			if vertical {
				return kbs[i].b.Y0 < kbs[j].b.Y0
			}
			return kbs[i].b.X0 < kbs[j].b.X0
		})
		fmt.Printf("  container %-6s %-6s children=%d tiling=%.0f%% (parent %dx%d)\n",
			pid, axis, len(kbs), tiling*100, p.Width(), p.Height())
		prevEnd := -1
		for _, k := range kbs {
			var ext, start, end int
			if vertical {
				ext, start, end = k.b.Height(), k.b.Y0, k.b.Y1
			} else {
				ext, start, end = k.b.Width(), k.b.X0, k.b.X1
			}
			frac := 0.0
			if span > 0 {
				frac = float64(ext) / float64(span) * 100
			}
			gap := 0
			if prevEnd >= 0 {
				gap = start - prevEnd
			}
			label := merged[idx[k.id]].Type.Value
			if merged[idx[k.id]].Text != nil {
				label += ":" + trunc(merged[idx[k.id]].Text.Value, 14)
			}
			fmt.Printf("    %-22s %5.1f%%  gapBefore=%dpx\n", label, frac, gap)
			prevEnd = end
		}
	}
}

func detectRegion(im *imageproc.Image, toks []ir.OCRToken) []ir.Component {
	comps, err := detect.RegionComponentsOpenCV(im.AsImage(), detect.DefaultOpenCVRegionOptions(), toks)
	if err != nil {
		return nil
	}
	return comps
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
