// Package raster draws anti-aliased rounded rectangles and soft shadows
// into a premultiplied BGRA pixel buffer, the format Windows wants for a
// per-pixel-alpha window. It is small on purpose: the hint window is a few
// rectangles, and drawing them here keeps the program free of GDI+.
package raster

import "math"

// Canvas is a top-down pixel buffer; each pixel is 0xAARRGGBB with colour
// already multiplied by alpha.
type Canvas struct {
	Pix  []uint32
	W, H int
}

// Color is a straight (not premultiplied) 0xAARRGGBB value.
type Color uint32

func RGB(hex uint32) Color { return Color(0xFF000000 | hex) }

// Alpha returns the colour with its opacity scaled by a (0..1).
func (c Color) Alpha(a float64) Color {
	return Color(uint32(float64(c>>24)*a+0.5)<<24 | uint32(c)&0xFFFFFF)
}

func (c *Canvas) Clear() {
	for i := range c.Pix {
		c.Pix[i] = 0
	}
}

// blend puts col over the pixel with the given coverage (0..1).
func (c *Canvas) blend(i int, col Color, cover float64) {
	a := float64(col>>24) / 255 * cover
	if a <= 0 {
		return
	}
	sr, sg, sb := float64(col>>16&0xFF)*a, float64(col>>8&0xFF)*a, float64(col&0xFF)*a
	d := c.Pix[i]
	k := 1 - a
	da, dr, dg, db := float64(d>>24), float64(d>>16&0xFF), float64(d>>8&0xFF), float64(d&0xFF)
	c.Pix[i] = uint32(a*255+da*k+0.5)<<24 | uint32(sr+dr*k+0.5)<<16 | uint32(sg+dg*k+0.5)<<8 | uint32(sb+db*k+0.5)
}

// dist is the signed distance from a point to a rounded rectangle
// (negative inside).
func dist(px, py, x0, y0, x1, y1, r float64) float64 {
	hx, hy := (x1-x0)/2, (y1-y0)/2
	qx := math.Abs(px-(x0+hx)) - (hx - r)
	qy := math.Abs(py-(y0+hy)) - (hy - r)
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}

func (c *Canvas) span(v0, v1 float64, limit int) (int, int) {
	a, b := int(math.Floor(v0)), int(math.Ceil(v1))
	if a < 0 {
		a = 0
	}
	if b > limit {
		b = limit
	}
	return a, b
}

// Fill draws a filled rounded rectangle.
func (c *Canvas) Fill(x0, y0, x1, y1, r float64, col Color) {
	if m := math.Min(x1-x0, y1-y0) / 2; r > m {
		r = m
	}
	ya, yb := c.span(y0, y1, c.H)
	xa, xb := c.span(x0, x1, c.W)
	for y := ya; y < yb; y++ {
		py := float64(y) + 0.5
		inY := py > y0+r && py < y1-r
		for x := xa; x < xb; x++ {
			px := float64(x) + 0.5
			cover := 1.0
			// Only the edges and corners need the distance.
			if !(inY && px > x0+1 && px < x1-1) && !(px > x0+r && px < x1-r && py > y0+1 && py < y1-1) {
				cover = 0.5 - dist(px, py, x0, y0, x1, y1, r)
				if cover <= 0 {
					continue
				}
				if cover > 1 {
					cover = 1
				}
			}
			c.blend(y*c.W+x, col, cover)
		}
	}
}

// Stroke draws the outline of a rounded rectangle, w pixels wide, inside it.
func (c *Canvas) Stroke(x0, y0, x1, y1, r, w float64, col Color) {
	ya, yb := c.span(y0, y1, c.H)
	xa, xb := c.span(x0, x1, c.W)
	for y := ya; y < yb; y++ {
		py := float64(y) + 0.5
		for x := xa; x < xb; x++ {
			px := float64(x) + 0.5
			if px > x0+r+w && px < x1-r-w && py > y0+w+1 && py < y1-w-1 {
				x = int(x1-r-w) - 1 // skip the hollow middle
				if x < xa {
					x = xa
				}
				continue
			}
			d := dist(px, py, x0, y0, x1, y1, r)
			cover := math.Min(0.5-d, 1) - math.Max(0, math.Min(0.5-(d+w), 1))
			if cover > 0 {
				c.blend(y*c.W+x, col, cover)
			}
		}
	}
}

// Shadow draws a soft shadow around a rounded rectangle: opacity alpha at
// its edge, fading to nothing over blur pixels.
func (c *Canvas) Shadow(x0, y0, x1, y1, r, blur float64, col Color) {
	ya, yb := c.span(y0-blur, y1+blur, c.H)
	xa, xb := c.span(x0-blur, x1+blur, c.W)
	for y := ya; y < yb; y++ {
		py := float64(y) + 0.5
		for x := xa; x < xb; x++ {
			if px := float64(x) + 0.5; px > x0+r && px < x1-r && py > y0+r && py < y1-r {
				x = int(x1 - r) // the middle is hidden behind the shape itself
				continue
			}
			d := dist(float64(x)+0.5, py, x0, y0, x1, y1, r)
			if d >= blur {
				continue
			}
			t := 1.0
			if d > 0 {
				t = 1 - d/blur
				t *= t
			}
			c.blend(y*c.W+x, col, t)
		}
	}
}

// Opaque sets full opacity in a rectangle. GDI text drawing clears the
// alpha channel where it draws; this puts it back.
func (c *Canvas) Opaque(x0, y0, x1, y1 int) {
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > c.W {
		x1 = c.W
	}
	if y1 > c.H {
		y1 = c.H
	}
	for y := y0; y < y1; y++ {
		row := c.Pix[y*c.W+x0 : y*c.W+x1]
		for i := range row {
			row[i] |= 0xFF000000
		}
	}
}
