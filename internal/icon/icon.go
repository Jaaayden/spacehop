// Package icon draws the SpaceHop icon at any size. The same code produces
// the tray icon at run time, the icon embedded in the executable and the
// images used by the settings page and the README, so they cannot drift.
//
// The mark: a ball hopping off a lime space bar, on an ink tile.
package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

type rgb struct{ r, g, b float64 }

var (
	tileTop  = rgb{0x2B, 0x2F, 0x3A}
	tileBot  = rgb{0x12, 0x14, 0x19}
	keyTop   = rgb{0xD4, 0xFA, 0x52}
	keyBot   = rgb{0xB5, 0xE8, 0x2C}
	keySide  = rgb{0x6E, 0x94, 0x10}
	ball     = rgb{0xF6, 0xF9, 0xEE}
	offKey   = rgb{0x9A, 0xA0, 0xAC}
	offSide  = rgb{0x5C, 0x61, 0x6C}
	offBall  = rgb{0xC9, 0xCD, 0xD6}
	sampling = 4
)

func mix(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

// inRound reports whether (x, y) lies in a rounded rectangle.
func inRound(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(x, x1-r))
	cy := math.Max(y0+r, math.Min(y, y1-r))
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

func inCircle(x, y, cx, cy, r float64) bool {
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

// sample returns the colour and opacity of the icon at a point of the unit
// square. small selects the simplified drawing used below 28 pixels.
func sample(x, y float64, small, off bool) (rgb, float64) {
	if !inRound(x, y, 0.03, 0.03, 0.97, 0.97, 0.225) {
		return rgb{}, 0
	}
	kt, kb, ks, bl := keyTop, keyBot, keySide, ball
	if off {
		kt, kb, ks, bl = offKey, offKey, offSide, offBall
	}
	// Space bar: a key cap seen slightly from above.
	x0, x1, top, bot, depth, r := 0.19, 0.81, 0.585, 0.735, 0.05, 0.065
	bx, by, br := 0.665, 0.285, 0.085
	if small {
		x0, x1, top, bot, depth, r = 0.16, 0.84, 0.56, 0.76, 0.0, 0.07
		bx, by, br = 0.66, 0.29, 0.12
	}
	if inCircle(x, y, bx, by, br) {
		return bl, 1
	}
	if inRound(x, y, x0, top, x1, bot, r) {
		return mix(kt, kb, (y-top)/(bot-top)), 1
	}
	if depth > 0 && inRound(x, y, x0, top+depth, x1, bot+depth, r) {
		return ks, 1
	}
	if !small {
		// The hop: dots along the arc from the bar up to the ball.
		for i, d := range [][3]float64{{0.335, 0.47, 0.026}, {0.405, 0.355, 0.033}, {0.515, 0.285, 0.040}} {
			if inCircle(x, y, d[0], d[1], d[2]) {
				return mix(tileBot, kt, 0.30+0.22*float64(i)), 1
			}
		}
	}
	// Tile, with a faint highlight along the top edge.
	c := mix(tileTop, tileBot, y)
	if y < 0.5 && !inRound(x, y, 0.03, 0.045, 0.97, 0.97, 0.225) {
		c = mix(c, rgb{255, 255, 255}, 0.16)
	}
	return c, 1
}

// Render draws the icon; off gives the paused (grey) variant.
func Render(size int, off bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	small := size < 28
	n := float64(sampling * sampling)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < sampling; sy++ {
				for sx := 0; sx < sampling; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/float64(sampling)) / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/float64(sampling)) / float64(size)
					if c, o := sample(x, y, small, off); o > 0 {
						r, g, b, a = r+c.r, g+c.g, b+c.b, a+1
					}
				}
			}
			if a > 0 {
				img.SetNRGBA(px, py, color.NRGBA{uint8(r/a + 0.5), uint8(g/a + 0.5), uint8(b/a + 0.5), uint8(a/n*255 + 0.5)})
			}
		}
	}
	return img
}

// PNG returns the icon as a PNG file.
func PNG(size int, off bool) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, Render(size, off))
	return buf.Bytes()
}

// Image returns the icon in the form Windows stores it: a PNG at 256
// pixels, an uncompressed bitmap with a mask below that.
func Image(size int) []byte {
	if size >= 256 {
		return PNG(size, false)
	}
	img := Render(size, false)
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, struct {
		Size          uint32
		Width, Height int32
		Planes, Bits  uint16
		Rest          [6]uint32
	}{Size: 40, Width: int32(size), Height: int32(size * 2), Planes: 1, Bits: 32})
	for y := size - 1; y >= 0; y-- { // bottom-up BGRA
		for x := 0; x < size; x++ {
			c := img.NRGBAAt(x, y)
			b.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	b.Write(make([]byte, ((size+31)/32*4)*size)) // empty mask: alpha decides
	return b.Bytes()
}

// ICO returns a Windows icon file holding the given sizes.
func ICO(sizes ...int) []byte {
	var head, body bytes.Buffer
	_ = binary.Write(&head, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for _, s := range sizes {
		data := Image(s)
		dim := byte(s) // 256 is stored as 0
		head.Write([]byte{dim, dim, 0, 0})
		_ = binary.Write(&head, binary.LittleEndian, [2]uint16{1, 32})
		_ = binary.Write(&head, binary.LittleEndian, [2]uint32{uint32(len(data)), uint32(offset)})
		body.Write(data)
		offset += len(data)
	}
	return append(head.Bytes(), body.Bytes()...)
}
