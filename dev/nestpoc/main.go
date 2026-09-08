//go:build opencv

// Command nestpoc is a THROWAWAY POC for layout-container / nesting detection.
// It runs region detection WITHOUT the filterNested flatten (via the POC hook)
// and with a relaxed MaxAreaFrac so large layout regions survive, then builds a
// containment TREE (each box's parent = smallest box that strictly contains it)
// and prints the hierarchy per page with depth. This tests: does un-suppressing
// the nesting the detector already sees yield real multi-level layout structure?
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
	maxArea := flag.Float64("max-area", 0.95, "MaxAreaFrac (relaxed so layout regions survive)")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: nestpoc [-max-area F] <image-or-dir>...")
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
		runPage(p, *maxArea)
	}
}

func isImg(n string) bool {
	n = strings.ToLower(n)
	return strings.HasSuffix(n, ".png") || strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".jpeg")
}

type node struct {
	b        ir.BoundingBox
	area     int
	children []int
	depth    int
}

func runPage(path string, maxArea float64) {
	im, err := imageproc.Load(path)
	if err != nil {
		return
	}
	opts := detect.DefaultOpenCVRegionOptions()
	opts.MaxAreaFrac = maxArea // let large layout regions survive
	boxes, err := detect.DetectRegionsOpenCVRawPOC(im.AsImage(), opts)
	if err != nil || len(boxes) == 0 {
		fmt.Printf("== %s == (no boxes)\n", filepath.Base(path))
		return
	}
	nodes := make([]node, len(boxes))
	for i, b := range boxes {
		nodes[i] = node{b: b.BBox, area: b.BBox.Area()}
	}
	// Parent = smallest strictly-containing box.
	parent := make([]int, len(nodes))
	for i := range nodes {
		parent[i] = -1
		bestArea := 1 << 62
		for j := range nodes {
			if i == j {
				continue
			}
			if nodes[j].b.Contains(nodes[i].b) && nodes[j].area > nodes[i].area && nodes[j].area < bestArea {
				parent[i] = j
				bestArea = nodes[j].area
			}
		}
	}
	var roots []int
	for i := range nodes {
		if parent[i] == -1 {
			roots = append(roots, i)
		} else {
			nodes[parent[i]].children = append(nodes[parent[i]].children, i)
		}
	}
	// Depth + max depth.
	maxDepth := 0
	var setDepth func(i, d int)
	setDepth = func(i, d int) {
		nodes[i].depth = d
		if d > maxDepth {
			maxDepth = d
		}
		for _, c := range nodes[i].children {
			setDepth(c, d+1)
		}
	}
	for _, r := range roots {
		setDepth(r, 0)
	}
	// Count nodes with >=2 children (real layout containers).
	containers := 0
	for i := range nodes {
		if len(nodes[i].children) >= 2 {
			containers++
		}
	}
	fmt.Printf("== %s ==  boxes=%d roots=%d maxDepth=%d containers(>=2 kids)=%d\n",
		filepath.Base(path), len(nodes), len(roots), maxDepth, containers)
	// Print the deepest / most-branching container as a sample.
	best := -1
	for i := range nodes {
		if len(nodes[i].children) >= 2 {
			if best == -1 || len(nodes[i].children) > len(nodes[best].children) {
				best = i
			}
		}
	}
	if best >= 0 {
		b := nodes[best].b
		fmt.Printf("   biggest container: %dx%d at (%d,%d) depth=%d children=%d\n",
			b.Width(), b.Height(), b.X0, b.Y0, nodes[best].depth, len(nodes[best].children))
	}
}
