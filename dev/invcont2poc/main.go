//go:build opencv

// Command invcont2poc is the SHARPENED invisible-container POC. Instead of
// clustering raw boxes (which over-reached — a column absorbed the search bar
// sharing its x-band), it SEEDS from RepeatedGroups (Milestone B): same-type,
// regularly-spaced sibling sets. The inferred container = the bounding box of a
// group's members, with:
//   - a same-type guarantee (RepeatedGroup members share a type),
//   - a regularity gate (GapSpread small relative to GapMedian => evenly spaced),
//   - member count >= 2,
// and it carries the group's Header (Milestone E) as the container name when
// present (e.g. "TO DO (4)"). Confidence = regularity. Prints inferred
// containers per page with axis, members, header, and confidence.
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
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: invcont2poc <image-or-dir>...")
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
		runPage(p)
	}
}

func isImg(n string) bool {
	n = strings.ToLower(n)
	return strings.HasSuffix(n, ".png") || strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".jpeg")
}

func runPage(path string) {
	im, err := imageproc.Load(path)
	if err != nil {
		return
	}
	toks, _ := ocr.NewTesseractEngine().Recognize(context.Background(), ocr.Input{ImagePath: path})
	region := detectRegion(im, toks)
	text := detect.TextComponentsFromOCR(toks, detect.DefaultTextComponentOptions())
	raw := append(append([]ir.Component{}, text...), region...)
	merged := dub.Reconcile(raw, dub.Options{IoUThreshold: 0.65, ConfidenceMerge: 0.5})

	groups := graph.DetectRepeatingGroups(merged, 50, 80, 2)
	graph.AssociateHeaders(groups, merged)
	graph.AttachGroupGaps(groups, merged)
	if len(groups) == 0 {
		return
	}
	idx := map[string]int{}
	for i := range merged {
		idx[merged[i].ID] = i
	}
	fmt.Printf("== %s ==  groups=%d\n", filepath.Base(path), len(groups))
	for _, g := range groups {
		// Confidence = regularity: 1 - GapSpread/GapMedian (clamped). Evenly
		// spaced => near 1; erratic => low. Withhold below 0.5.
		conf := 1.0
		if g.GapMedian > 0 {
			conf = 1.0 - float64(g.GapSpread)/float64(g.GapMedian)
		}
		if conf < 0 {
			conf = 0
		}
		gate := "OK"
		if conf < 0.5 || len(g.MemberIDs) < 2 {
			gate = "withhold"
		}
		box := boundIDs(merged, idx, g.MemberIDs)
		kind := "ROW"
		if g.Axis == "y" {
			kind = "COLUMN"
		}
		hdr := ""
		if g.Header != "" {
			hdr = fmt.Sprintf(" header=%q", g.Header)
		}
		fmt.Printf("  %-6s %-6s %dx%d at (%d,%d) members=%d type=%s gapMed=%d gapSpread=%d conf=%.2f [%s]%s\n",
			kind, g.Axis, box.Width(), box.Height(), box.X0, box.Y0,
			len(g.MemberIDs), g.Type, g.GapMedian, g.GapSpread, conf, gate, hdr)
	}
}

func boundIDs(comps []ir.Component, idx map[string]int, ids []string) ir.BoundingBox {
	var out ir.BoundingBox
	first := true
	for _, id := range ids {
		i, ok := idx[id]
		if !ok {
			continue
		}
		b := comps[i].BBox
		if first {
			out = b
			first = false
			continue
		}
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

func detectRegion(im *imageproc.Image, toks []ir.OCRToken) []ir.Component {
	comps, err := detect.RegionComponentsOpenCV(im.AsImage(), detect.DefaultOpenCVRegionOptions(), toks)
	if err != nil {
		return nil
	}
	return comps
}
