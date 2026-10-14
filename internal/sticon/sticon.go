// Package sticon draws the SliceTrigger "ST" icon, optionally with a status
// dot, and encodes multi-size .ico files.
package sticon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

var (
	bgTop    = color.NRGBA{0x1F, 0x4E, 0x8C, 0xFF}
	bgBottom = color.NRGBA{0x0B, 0x22, 0x44, 0xFF}
)

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// roundedRectSD is the signed distance from (x, y) to a rounded square of
// side n with corner radius r (negative inside).
func roundedRectSD(x, y, n, r float64) float64 {
	cx, cy := math.Abs(x-n/2)-(n/2-r), math.Abs(y-n/2)-(n/2-r)
	out := math.Hypot(math.Max(cx, 0), math.Max(cy, 0))
	return out + math.Min(math.Max(cx, cy), 0) - r
}

func blend(dst color.NRGBA, src color.NRGBA, a float64) color.NRGBA {
	sa := float64(src.A) / 255 * a
	da := float64(dst.A) / 255
	oa := sa + da*(1-sa)
	if oa == 0 {
		return color.NRGBA{}
	}
	mix := func(s, d uint8) uint8 {
		return uint8((float64(s)*sa + float64(d)*da*(1-sa)) / oa)
	}
	return color.NRGBA{mix(src.R, dst.R), mix(src.G, dst.G), mix(src.B, dst.B), uint8(oa * 255)}
}

// Draw renders the icon at n x n pixels. If dot is non-nil a status light in
// that colour is drawn in the bottom-right corner.
func Draw(n int, dot color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	fn := float64(n)
	r := fn * 0.2
	for y := 0; y < n; y++ {
		t := float64(y) / fn
		bg := color.NRGBA{
			uint8(float64(bgTop.R)*(1-t) + float64(bgBottom.R)*t),
			uint8(float64(bgTop.G)*(1-t) + float64(bgBottom.G)*t),
			uint8(float64(bgTop.B)*(1-t) + float64(bgBottom.B)*t),
			0xFF,
		}
		for x := 0; x < n; x++ {
			a := clamp01(0.5 - roundedRectSD(float64(x)+0.5, float64(y)+0.5, fn, r))
			if a > 0 {
				img.SetNRGBA(x, y, blend(img.NRGBAAt(x, y), bg, a))
			}
		}
	}

	if f, err := opentype.Parse(gobold.TTF); err == nil {
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: fn * 0.56, DPI: 72, Hinting: font.HintingNone})
		if err == nil {
			// Draw onto a mask, then composite, so the text is anti-aliased
			// against the background.
			mask := image.NewAlpha(img.Bounds())
			d := &font.Drawer{Dst: mask, Src: image.Opaque, Face: face}
			b, _ := d.BoundString("ST")
			half := fixed.I(n) / 2
			d.Dot = fixed.Point26_6{X: half - (b.Min.X+b.Max.X)/2, Y: half - (b.Min.Y+b.Max.Y)/2}
			if dot != nil {
				d.Dot.Y -= fixed.I(n) / 16 // leave room for the light
				d.Dot.X -= fixed.I(n) / 32
			}
			d.DrawString("ST")
			face.Close()
			white := color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
			for y := 0; y < n; y++ {
				for x := 0; x < n; x++ {
					if a := mask.AlphaAt(x, y).A; a > 0 {
						img.SetNRGBA(x, y, blend(img.NRGBAAt(x, y), white, float64(a)/255))
					}
				}
			}
		}
	}

	if dot != nil {
		dc := color.NRGBAModel.Convert(dot).(color.NRGBA)
		dc.A = 0xFF
		ring := color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
		cx, cy, rad := fn*0.76, fn*0.76, fn*0.2
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
				if a := clamp01(rad + 0.5 - d); a > 0 {
					img.SetNRGBA(x, y, blend(img.NRGBAAt(x, y), ring, a))
				}
				if a := clamp01(rad*0.78 + 0.5 - d); a > 0 {
					img.SetNRGBA(x, y, blend(img.NRGBAAt(x, y), dc, a))
				}
			}
		}
	}
	return img
}

// ICO encodes the plain icon at the given sizes as a .ico file with
// PNG-compressed entries.
func ICO(sizes []int) ([]byte, error) {
	var pngs [][]byte
	for _, n := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, Draw(n, nil)); err != nil {
			return nil, err
		}
		pngs = append(pngs, buf.Bytes())
	}
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, n := range sizes {
		dim := uint8(n)
		if n >= 256 {
			dim = 0
		}
		binary.Write(&out, binary.LittleEndian, struct {
			W, H, Colors, Reserved uint8
			Planes, BPP            uint16
			Size, Offset           uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(pngs[i])), uint32(offset)})
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		out.Write(p)
	}
	return out.Bytes(), nil
}
