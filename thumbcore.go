package main

// Bélyegképek: kép beolvasása és kicsinyítése (közös a két felületben).

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

func isImageName(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "jpg", "jpeg", "png", "gif", "bmp", "webp":
		return true
	}
	return false
}

func makeThumb(path string, size int) image.Image {
	st, err := os.Stat(path)
	if err != nil || st.Size() > 80<<20 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	return scaleToFit(src, size)
}

func scaleToFit(src image.Image, size int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	if w <= size && h <= size {
		return src
	}
	nw, nh := size, size
	if w > h {
		nh = h * size / w
	} else {
		nw = w * size / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	return dst
}
