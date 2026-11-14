package main

// Az eszköztár ikonjainak rajzolása képbe (Windowson ebből lesz az ikon; Linuxon a
// Fyne maga rajzolja ugyanezeket az svg-ket). Közös, hogy tesztelhető legyen.

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"math"

	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
)

//go:embed tbicons/*.svg
var tbIconFS embed.FS

// tileImage: px×px képpontos, lekerekített színes lapka, rajta fehér ikon – ugyanolyan
// arányokkal, mint a Linux-eszköztáron (lapka a teljes méret, ikon a 68%-a).
// Ha az ikon nem található, üres lapka.
func tileImage(name string, tile color.NRGBA, px int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, px, px))
	m := float64(px) / 16 // lapka margója (mint eddig a Windows-ikonoknál)
	rad := (float64(px) - 2*m) * 0.22
	x0, y0, x1, y1 := m, m, float64(px)-m, float64(px)-m
	ink := svgAlpha(name, px, 0.68*(float64(px)-2*m))
	for y := 0; y < px; y++ {
		for x := 0; x < px; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			dx := math.Max(math.Max(x0+rad-fx, fx-(x1-rad)), 0)
			dy := math.Max(math.Max(y0+rad-fy, fy-(y1-rad)), 0)
			d := math.Sqrt(dx*dx+dy*dy) - rad
			a := math.Min(math.Max(0.5-d, 0), 1)
			k := 0.0
			if ink != nil {
				k = float64(ink.AlphaAt(x, y).A) / 255
			}
			mix := func(c uint8) uint8 { return uint8(float64(c)*(1-k) + 255*k + 0.5) }
			img.SetNRGBA(x, y, color.NRGBA{mix(tile.R), mix(tile.G), mix(tile.B), uint8(a*255 + 0.5)})
		}
	}
	return img
}

// svgAlpha: az svg ikon takarása px×px képen, középre, size méretben.
func svgAlpha(name string, px int, size float64) *image.Alpha {
	data, err := tbIconFS.ReadFile("tbicons/" + name + ".svg")
	if err != nil {
		return nil
	}
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data), oksvg.IgnoreErrorMode)
	if err != nil {
		return nil
	}
	off := (float64(px) - size) / 2
	icon.SetTarget(off, off, size, size)
	rgba := image.NewRGBA(image.Rect(0, 0, px, px))
	icon.Draw(rasterx.NewDasher(px, px, rasterx.NewScannerGV(px, px, rgba, rgba.Bounds())), 1)
	out := image.NewAlpha(rgba.Bounds())
	for i := 0; i < px*px; i++ {
		out.Pix[i] = rgba.Pix[i*4+3]
	}
	return out
}
