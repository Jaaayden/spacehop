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
	// Clip limits drawing to x0, y0, x1, y1 when x1 > x0.
	Clip [4]int
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
// (negative inside). It is called for every edge pixel, so it avoids the
// general-purpose math helpers.
func dist(px, py, x0, y0, x1, y1, r float64) float64 {
	hx, hy := (x1-x0)/2, (y1-y0)/2
	qx := px - (x0 + hx)
	if qx < 0 {
		qx = -qx
	}
	qy := py - (y0 + hy)
	if qy < 0 {
		qy = -qy
	}
	qx -= hx - r
	qy -= hy - r
	switch {
	case qx > 0 && qy > 0:
		return math.Sqrt(qx*qx+qy*qy) - r
	case qx > qy:
		return qx - r
	}
	return qy - r
}

// span returns the pixel range covered on one axis (0 = x, 1 = y).
func (c *Canvas) span(v0, v1 float64, limit, axis int) (int, int) {
	a, b := int(math.Floor(v0)), int(math.Ceil(v1))
	if a < 0 {
		a = 0
	}
	if b > limit {
		b = limit
	}
	if c.Clip[2] > c.Clip[0] {
		if a < c.Clip[axis] {
			a = c.Clip[axis]
		}
		if b > c.Clip[axis+2] {
			b = c.Clip[axis+2]
		}
	}
	return a, b
}

// over puts an opaque-coverage colour over a run of pixels using integer
// arithmetic; it is the inner loop of every large fill.
func over(row []uint32, col Color) {
	a := uint32(col >> 24)
	if a == 0xFF {
		for i := range row {
			row[i] = uint32(col)
		}
		return
	}
	if a == 0 {
		return
	}
	// Two channels at a time: red and blue share one multiply, alpha and
	// green the other. Dividing by 256 instead of 255 is off by at most one
	// level, which cannot be seen.
	k := 256 - a - a>>7
	a1 := a + a>>7
	srb := (uint32(col) & 0x00FF00FF) * a1
	sag := (0xFF0000 | uint32(col)>>8&0xFF) * a1
	for i, d := range row {
		rb := (srb + (d&0x00FF00FF)*k) >> 8 & 0x00FF00FF
		ag := (sag + (d>>8&0x00FF00FF)*k) & 0xFF00FF00
		row[i] = rb | ag
	}
}

// Fill draws a filled rounded rectangle.
func (c *Canvas) Fill(x0, y0, x1, y1, r float64, col Color) {
	if m := math.Min(x1-x0, y1-y0) / 2; r > m {
		r = m
	}
	ya, yb := c.span(y0, y1, c.H, 1)
	xa, xb := c.span(x0, x1, c.W, 0)
	if xb <= xa {
		return
	}
	for y := ya; y < yb; y++ {
		py := float64(y) + 0.5
		// The run of pixels that is certainly fully covered on this row.
		in0, in1 := xa, xa
		if py > y0+1 && py < y1-1 {
			inset := 1.0
			// In the rows of the corners the covered run follows the arc.
			dy := 0.0
			if py < y0+r {
				dy = y0 + r - py
			} else if py > y1-r {
				dy = py - (y1 - r)
			}
			if dy > 0 {
				inset = r + 1
				if rr := (r - 1) * (r - 1); rr > dy*dy && r > 1 {
					inset = r - math.Sqrt(rr-dy*dy) + 1
				}
			}
			in0, in1 = int(math.Ceil(x0+inset)), int(math.Floor(x1-inset))
			if in0 < xa {
				in0 = xa
			}
			if in1 > xb {
				in1 = xb
			}
			if in1 > in0 {
				over(c.Pix[y*c.W+in0:y*c.W+in1], col)
			} else {
				in0, in1 = xa, xa
			}
		}
		for x := xa; x < xb; x++ {
			if x == in0 && in1 > in0 {
				x = in1 - 1
				continue
			}
			cover := 0.5 - dist(float64(x)+0.5, py, x0, y0, x1, y1, r)
			if cover <= 0 {
				continue
			}
			if cover > 1 {
				cover = 1
			}
			c.blend(y*c.W+x, col, cover)
		}
	}
}

// Stroke draws the outline of a rounded rectangle, w pixels wide, inside it.
// The straight sides are plain runs of pixels; only the corners need the
// distance to the curve.
func (c *Canvas) Stroke(x0, y0, x1, y1, r, w float64, col Color) {
	if m := math.Min(x1-x0, y1-y0) / 2; r > m {
		r = m
	}
	ya, yb := c.span(y0, y1, c.H, 1)
	xa, xb := c.span(x0, x1, c.W, 0)
	k := r + w + 1 // size of the corner boxes
	for y := ya; y < yb; y++ {
		py := float64(y) + 0.5
		corner := py < y0+k || py > y1-k
		for x := xa; x < xb; x++ {
			px := float64(x) + 0.5
			if px > x0+k && px < x1-k {
				// Between the corners: the top and bottom sides are runs,
				// the rows in between are empty.
				run := c.Pix[y*c.W+x : y*c.W+min(xb, int(x1-k))+0]
				if len(run) > 0 {
					if cover := math.Min(py-y0, y1-py); corner && cover < w+0.5 {
						if cover >= w-0.5 && cover > 0.5 {
							over(run, col.Alpha(w+0.5-cover))
						} else if cover >= 0.5 {
							over(run, col)
						} else {
							over(run, col.Alpha(cover+0.5))
						}
					}
					x += len(run) - 1
					continue
				}
			}
			if !corner && px > x0+w+1 && px < x1-w-1 {
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
	ya, yb := c.span(y0-blur, y1+blur, c.H, 1)
	xa, xb := c.span(x0-blur, x1+blur, c.W, 0)
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
