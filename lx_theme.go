//go:build linux

package main

// Téma Linuxon: világos, sötét vagy automatikus (a rendszer beállítása szerint –
// a Fyne az asztali környezet „color-scheme” beállítását követi).

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

const (
	colFolder fyne.ThemeColorName = "gozie-folder"
	colWhite  fyne.ThemeColorName = "gozie-white"
)

type gzTheme struct {
	a *App
}

func (t *gzTheme) variant(v fyne.ThemeVariant) fyne.ThemeVariant {
	switch t.a.themeMode() {
	case "dark":
		return theme.VariantDark
	case "light":
		return theme.VariantLight
	}
	return v
}

func (t *gzTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	v = t.variant(v)
	switch n {
	case colFolder:
		if v == theme.VariantDark {
			return rgb(230, 190, 80)
		}
		return rgb(222, 165, 30)
	case colWhite:
		return rgb(255, 255, 255)
	}
	return theme.DefaultTheme().Color(n, v)
}

func (t *gzTheme) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (t *gzTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }
func (t *gzTheme) Size(n fyne.ThemeSizeName) float32 {
	s := theme.DefaultTheme().Size(n)
	if n == theme.SizeNameText || n == theme.SizeNameCaptionText {
		if f := t.a.uiScale; f > 0 {
			s *= f
		}
	}
	return s
}

func (a *App) themeMode() string {
	switch v := a.setting("theme", "auto"); v {
	case "dark", "light", "auto":
		return v
	}
	return "auto"
}

// isDark: a most látható téma sötét-e.
func (a *App) isDark() bool {
	switch a.themeMode() {
	case "dark":
		return true
	case "light":
		return false
	}
	return a.fa.Settings().ThemeVariant() == theme.VariantDark
}

// tightTheme: a fájllistában nincs térköz a sorok között.
type tightTheme struct{ fyne.Theme }

func (t tightTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNamePadding, theme.SizeNameInnerPadding, theme.SizeNameSeparatorThickness:
		return 0
	}
	return t.Theme.Size(n)
}
