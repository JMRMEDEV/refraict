//go:build opencv

package cli

import (
	"image"
	"log/slog"

	"github.com/refraict/refraict/internal/detect"
	"github.com/refraict/refraict/internal/ir"
)

// detectRegionComponents runs the OpenCV Canny-based region detector, built
// only when compiled with `-tags opencv`. It handles low-contrast flat UIs that
// the pure-Go detector cannot segment (faint card borders), at the cost of a
// system OpenCV dependency.
func detectRegionComponents(img image.Image, toks []ir.OCRToken) []ir.Component {
	comps, err := detect.RegionComponentsOpenCV(img, detect.DefaultOpenCVRegionOptions(), toks)
	if err != nil {
		slog.Warn("opencv region detection failed", "err", err)
		return nil
	}
	return comps
}
