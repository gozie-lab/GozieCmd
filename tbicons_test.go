package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"testing"
)

// Minden parancsikon megvan és rajzolható (a lapka közepén van fehér jel).
func TestToolbarIcons(t *testing.T) {
	var ids []string
	for id := range tbIconFile {
		ids = append(ids, id)
	}
	sheet := image.NewNRGBA(image.Rect(0, 0, 48*10, 48*((len(ids)+9)/10)))
	draw.Draw(sheet, sheet.Bounds(), image.NewUniform(color.NRGBA{40, 40, 40, 255}), image.Point{}, draw.Src)
	for i, id := range ids {
		r, g, b := tbTile(id)
		img := tileImage(tbIconFile[id], color.NRGBA{r, g, b, 255}, 48)
		white := 0
		for y := 0; y < 48; y++ {
			for x := 0; x < 48; x++ {
				if c := img.NRGBAAt(x, y); c.A == 255 && int(c.R)+int(c.G)+int(c.B)-int(r)-int(g)-int(b) > 150 {
					white++
				}
			}
		}
		if white < 10 {
			t.Errorf("%s (%s): nincs látható jel", id, tbIconFile[id])
		}
		draw.Draw(sheet, image.Rect((i%10)*48, (i/10)*48, (i%10)*48+48, (i/10)*48+48), img, image.Point{}, draw.Over)
	}
	if p := os.Getenv("GZ_ICON_SHEET"); p != "" {
		f, _ := os.Create(p)
		png.Encode(f, sheet)
		f.Close()
	}
}
