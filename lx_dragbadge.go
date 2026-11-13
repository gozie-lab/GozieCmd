//go:build linux

package main

// Húzás közben az egér mellett egy kis címke mutatja, mi mozog és hová kerül
// (mint Windowson a húzott fájlok képe). A címke egy külön, a teljes ablakot lefedő
// rétegen van, de nem fogad egéreseményt, így a húzást nem zavarja.

import (
	"image/color"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
)

type dragBadge struct {
	box   *fyne.Container
	bg    *canvas.Rectangle
	icon  *canvas.Image
	what  *canvas.Text
	where *canvas.Text
	layer *fyne.Container
}

func newDragBadge() *dragBadge {
	b := &dragBadge{
		bg:    canvas.NewRectangle(color.Black),
		icon:  canvas.NewImageFromResource(theme.ContentCopyIcon()),
		what:  canvas.NewText("", color.White),
		where: canvas.NewText("", color.White),
	}
	b.bg.CornerRadius = 6
	b.bg.StrokeWidth = 1
	b.icon.FillMode = canvas.ImageFillContain
	b.icon.SetMinSize(fyne.NewSize(18, 18))
	b.what.TextStyle = fyne.TextStyle{Bold: true}
	texts := container.NewVBox(b.what, b.where)
	inner := container.NewHBox(container.NewCenter(b.icon), texts)
	b.box = container.NewStack(b.bg, container.New(layout.NewCustomPaddedLayout(5, 5, 8, 10), inner))
	b.box.Hide()
	b.layer = container.NewWithoutLayout(b.box)
	return b
}

// show a címkét az egér mellé teszi. ok: ejthető-e ide.
func (b *dragBadge) show(pos fyne.Position, what, where string, move, ok bool) {
	ts := theme.TextSize()
	b.what.TextSize, b.where.TextSize = ts, ts*0.9
	b.what.Text, b.where.Text = what, where
	fg := theme.Color(theme.ColorNameForeground)
	b.what.Color, b.where.Color = fg, fg
	b.bg.FillColor = theme.Color(theme.ColorNameOverlayBackground)
	b.bg.StrokeColor = theme.Color(theme.ColorNamePrimary)
	switch {
	case !ok:
		b.icon.Resource = theme.NewDisabledResource(theme.CancelIcon())
		b.where.Color = theme.Color(theme.ColorNameDisabled)
		b.bg.StrokeColor = theme.Color(theme.ColorNameDisabled)
	case move:
		b.icon.Resource = theme.NewPrimaryThemedResource(theme.ContentCutIcon())
	default:
		b.icon.Resource = theme.NewPrimaryThemedResource(theme.ContentCopyIcon())
	}
	b.icon.Refresh()
	b.what.Refresh()
	b.where.Refresh()
	b.bg.Refresh()
	sz := b.box.MinSize()
	b.box.Resize(sz)
	// az egér jobb alsó oldalára, de az ablakon belül
	x, y := pos.X+18, pos.Y+14
	if ls := b.layer.Size(); ls.Width > 0 {
		if x+sz.Width > ls.Width {
			x = pos.X - sz.Width - 8
		}
		if y+sz.Height > ls.Height {
			y = pos.Y - sz.Height - 8
		}
	}
	b.box.Move(fyne.NewPos(x, y))
	b.box.Show()
	b.layer.Refresh()
}

func (b *dragBadge) hide() {
	if b.box.Visible() {
		b.box.Hide()
		b.layer.Refresh()
	}
}

// updateDragBadge: a húzás aktuális állapota a címkén.
func (p *Panel) updateDragBadge(pos fyne.Position) {
	a := p.app
	if a.badge == nil {
		return
	}
	items := p.dragItems()
	if len(items) == 0 {
		a.badge.hide()
		return
	}
	move := p.mods()&fyne.KeyModifierShift != 0
	o := a.other(p)
	verb := T("Másolás ide:")
	if move {
		verb = T("Mozgatás ide:")
	}
	what := describe(items)
	if !o.contains(pos) {
		a.badge.show(pos, what, T("ejtsd a másik panelre"), move, false)
		return
	}
	target := o.dropTargetName()
	a.badge.show(pos, what, verb+" "+target, move, true)
}

// dragItems: a húzott elemek (a jelöltek, ha a húzás jelölt elemen indult).
func (p *Panel) dragItems() []*Entry {
	if p.dragStart < 0 || p.dragStart >= len(p.items) {
		return nil
	}
	e := p.items[p.dragStart]
	if e.IsParent {
		return nil
	}
	if p.marked[e.Name] {
		return p.selectedEntries()
	}
	return []*Entry{e}
}

// dropTargetName: hová kerülne az ejtett elem (a kiemelt mappa vagy a panel mappája).
func (p *Panel) dropTargetName() string {
	if p.dropRow >= 0 && p.dropRow < len(p.items) {
		return p.items[p.dropRow].Name
	}
	if p.vfs != nil {
		return p.vfs.Display(p.vpath)
	}
	if n := filepath.Base(p.path); n != "" && n != "/" && n != "." {
		return n
	}
	return p.path
}

