//go:build !opencv

package cli

import (
	"image"

	"github.com/refraict/refraict/internal/detect"
	"github.com/refraict/refraict/internal/ir"
)

// detectRegionComponents runs the pure-Go connected-components region detector.
// This is the default build; it keeps the binary statically linkable with no
// OpenCV dependency. Building with `-tags opencv` swaps in the stronger OpenCV
// Canny-based detector (see regions_opencv.go).
func detectRegionComponents(img image.Image, toks []ir.OCRToken) []ir.Component {
	return detect.RegionComponents(img, detect.DefaultRegionOptions(), toks)
}
