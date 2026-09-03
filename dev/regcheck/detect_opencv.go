package main

import (
	"image"

	"github.com/refraict/refraict/internal/detect"
	"github.com/refraict/refraict/internal/ir"
)

func detectComponents(img image.Image) []ir.Component {
	comps, err := detect.RegionComponentsOpenCV(img, detect.DefaultOpenCVRegionOptions(), nil)
	if err != nil {
		panic(err)
	}
	return comps
}
