//go:build linux

package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed goziecmd.png
var appIconPNG []byte

var appIconRes = fyne.NewStaticResource("goziecmd.png", appIconPNG)
