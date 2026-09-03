package cli

import (
	"image"
	"log/slog"

	"github.com/refraict/refraict/internal/detect"
	"github.com/refraict/refraict/internal/ir"
)

// detectRegionComponents runs the OpenCV Canny + CLAHE dual-pass region detector.
// OpenCV 4.x is a hard dependency of refraict (see README): the detector handles
// low-contrast/flat UIs (faint card borders) that a pure-Go detector cannot
// segment, and card detection underpins the structural-assembly signals
// (containment, repeated groups, headers). On detector error it logs and returns
// nil so the rest of the deterministic pipeline still runs.
func detectRegionComponents(img image.Image, toks []ir.OCRToken) []ir.Component {
	comps, err := detect.RegionComponentsOpenCV(img, detect.DefaultOpenCVRegionOptions(), toks)
	if err != nil {
		slog.Warn("region detection failed", "err", err)
		return nil
	}
	return comps
}
