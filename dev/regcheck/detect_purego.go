//go:build !opencv

package main

import (
	"image"

	"github.com/refraict/refraict/internal/detect"
	"github.com/refraict/refraict/internal/ir"
)

func detectComponents(img image.Image) []ir.Component {
	return detect.RegionComponents(img, detect.DefaultRegionOptions())
}
