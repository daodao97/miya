// Command ios-icon draws the iOS app icon from the desktop one: iOS wants a
// full-bleed opaque square and masks the corners itself, while the desktop
// icon is a rounded square with a transparent margin.
//
//	go run ./scripts/ios-icon
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"

	xdraw "golang.org/x/image/draw"
)

func main() {
	src, dst := "resources/icon.png", "resources/ios/app-icon.png"
	f, err := os.Open(src)
	if err != nil {
		log.Fatal(err)
	}
	decoded, err := png.Decode(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}
	b := decoded.Bounds()
	im := image.NewNRGBA(b)
	xdraw.Draw(im, b, decoded, b.Min, xdraw.Src)

	// The opaque square's bounds.
	box := image.Rectangle{Min: b.Max, Max: b.Min}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if im.NRGBAAt(x, y).A > 0 {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if box.Empty() {
		log.Fatal("icon is empty")
	}
	// Keep it square, centered on the drawing.
	side := max(box.Dx(), box.Dy())
	cx, cy := (box.Min.X+box.Max.X)/2, (box.Min.Y+box.Max.Y)/2
	sq := image.Rect(cx-side/2, cy-side/2, cx-side/2+side, cy-side/2+side).Intersect(b)

	// Fill the transparent corners with the colour the drawing has nearest
	// to them toward the middle, so the background's gradient carries on.
	out := image.NewRGBA(image.Rect(0, 0, sq.Dx(), sq.Dy()))
	fx, fy := float64(sq.Min.X+sq.Max.X)/2, float64(sq.Min.Y+sq.Max.Y)/2
	for y := sq.Min.Y; y < sq.Max.Y; y++ {
		for x := sq.Min.X; x < sq.Max.X; x++ {
			p := im.NRGBAAt(x, y)
			bg := p
			if p.A < 255 {
				dx, dy := fx-float64(x), fy-float64(y)
				n := math.Hypot(dx, dy)
				dx, dy = dx/n, dy/n
				for s := 1.0; s < n; s++ {
					q := im.NRGBAAt(x+int(math.Round(dx*s)), y+int(math.Round(dy*s)))
					if q.A == 255 {
						bg = q
						break
					}
				}
			}
			a := float64(p.A) / 255
			mix := func(fg, back uint8) uint8 { return uint8(math.Round(float64(fg)*a + float64(back)*(1-a))) }
			out.SetRGBA(x-sq.Min.X, y-sq.Min.Y, color.RGBA{mix(p.R, bg.R), mix(p.G, bg.G), mix(p.B, bg.B), 255})
		}
	}

	final := image.NewRGBA(image.Rect(0, 0, 1024, 1024))
	xdraw.CatmullRom.Scale(final, final.Bounds(), out, out.Bounds(), xdraw.Src, nil)
	for i := 3; i < len(final.Pix); i += 4 {
		final.Pix[i] = 255
	}
	w, err := os.Create(dst)
	if err != nil {
		log.Fatal(err)
	}
	if err := png.Encode(w, final); err != nil {
		log.Fatal(err)
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}
	log.Printf("%s: cropped %v from %s", dst, sq, src)
}
