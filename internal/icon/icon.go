// Package icon draws the SpaceHop icon at any size. The same code produces
// the tray icon at run time, the icon embedded in the executable and the
// images used by the settings page and the README, so they cannot drift.
//
// The mark: a ringed planet with a small moon hopping over it.
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
	skyTop   = rgb{0x2C, 0x31, 0x40}
	skyBot   = rgb{0x0E, 0x10, 0x15}
	limeTop  = rgb{0xE0, 0xFE, 0x6E}
	limeBot  = rgb{0xA6, 0xDC, 0x1E}
	limeDeep = rgb{0x5E, 0x86, 0x0C}
	ringCol  = rgb{0xF5, 0xF8, 0xEC}
	greyTop  = rgb{0xC4, 0xC8, 0xD2}
	greyBot  = rgb{0x8A, 0x90, 0x9C}
	greyDeep = rgb{0x55, 0x5A, 0x66}
	sampling = 4
)

func mix(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

// inSquircle reports whether a point lies in a superellipse filling the
// unit square up to the given margin: rounder than a rounded rectangle,
// with no flat-to-curve seam.
func inSquircle(x, y, margin float64) bool {
	h := 0.5 - margin
	u, v := math.Abs(x-0.5)/h, math.Abs(y-0.5)/h
	return math.Pow(u, 5)+math.Pow(v, 5) <= 1
}

func inCircle(x, y, cx, cy, r float64) bool {
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

const ringTilt = -22 * math.Pi / 180

// sample returns the colour and opacity of the icon at a point of the unit
// square. small selects the heavier drawing used below 28 pixels.
//
// The mark is a ringed planet in a night sky, with a small moon hopping
// over it: "space" and "hop".
func sample(x, y float64, small, off bool) (rgb, float64) {
	if !inSquircle(x, y, 0.02) {
		return rgb{}, 0
	}
	top, bot, deep := limeTop, limeBot, limeDeep
	if off {
		top, bot, deep = greyTop, greyBot, greyDeep
	}
	// Planet, ring (outer and inner radii along and across), moon.
	px, py, pr := 0.475, 0.575, 0.200
	ra, rb, ia, ib := 0.405, 0.128, 0.330, 0.076
	mx, my, mr := 0.725, 0.245, 0.058
	if small {
		pr = 0.215
		ra, rb, ia, ib = 0.42, 0.150, 0.315, 0.070
		mx, my, mr = 0.735, 0.235, 0.085
	}
	// Ring coordinates: u along the ring, v across it; v > 0 is the near side.
	dx, dy := x-px, y-py
	u := dx*math.Cos(ringTilt) + dy*math.Sin(ringTilt)
	v := -dx*math.Sin(ringTilt) + dy*math.Cos(ringTilt)
	band := func(shift float64) bool {
		w := v - shift
		return u*u/(ra*ra)+w*w/(rb*rb) <= 1 && u*u/(ia*ia)+w*w/(ib*ib) > 1
	}
	inPlanet := inCircle(x, y, px, py, pr)
	if inCircle(x, y, mx, my, mr) {
		return ringCol, 1
	}
	if band(0) && (v > 0 || !inPlanet) {
		return ringCol, 1
	}
	if inPlanet {
		c := mix(top, bot, (y-(py-pr))/(2*pr))
		// The ring's shadow on the planet, and a darker limb away from the light.
		if !small && band(0.034) && v > 0 {
			c = mix(c, deep, 0.55)
		}
		if !inCircle(x, y, px-0.035, py-0.04, pr) {
			c = mix(c, deep, 0.5)
		}
		return c, 1
	}
	c := mix(skyTop, skyBot, y)
	if !small {
		// The path of the hop, and a few stars.
		for _, d := range [][4]float64{{0.615, 0.205, 0.017, 0.50}, {0.530, 0.212, 0.012, 0.32},
			{0.215, 0.255, 0.012, 0.45}, {0.815, 0.640, 0.010, 0.35}, {0.285, 0.815, 0.008, 0.30}} {
			if inCircle(x, y, d[0], d[1], d[2]) {
				return mix(c, ringCol, d[3]), 1
			}
		}
	}
	// A soft light along the upper edge gives the tile some body.
	if !inSquircle(x, y-0.02, 0.02) {
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
