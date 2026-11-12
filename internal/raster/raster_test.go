package raster

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"
)

func TestDraw(t *testing.T) {
	c := &Canvas{W: 640, H: 420, Pix: make([]uint32, 640*420)}
	start := time.Now()
	c.Shadow(24, 30, 616, 396, 14, 24, RGB(0).Alpha(0.35))
	c.Fill(24, 24, 616, 390, 14, RGB(0x17191E))
	c.Stroke(24, 24, 616, 390, 14, 1, RGB(0xFFFFFF).Alpha(0.1))
	for i := 0; i < 24; i++ {
		x, y := float64(40+(i/8)*190), float64(70+(i%8)*38)
		c.Fill(x, y, x+30, y+28, 7, RGB(0x262A33))
		c.Stroke(x, y, x+30, y+28, 7, 1, RGB(0x3A3F4B))
	}
	took := time.Since(start)
	t.Logf("hint-sized frame: %v", took)
	if took > 100*time.Millisecond {
		t.Fatalf("too slow: %v", took)
	}
	// Inside is opaque, a corner pixel outside the shape only has shadow.
	if a := c.Pix[200*640+300] >> 24; a != 255 {
		t.Fatalf("centre alpha %d", a)
	}
	if a := c.Pix[25*640+25] >> 24; a > 200 {
		t.Fatalf("corner alpha %d", a)
	}
	if out := os.Getenv("RASTER_PNG"); out != "" {
		img := image.NewNRGBA(image.Rect(0, 0, c.W, c.H))
		for i, p := range c.Pix {
			a := p >> 24
			if a == 0 {
				img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = 200, 205, 215, 255
				continue
			}
			bg := uint32(210) * (255 - a) / 255
			img.Set(i%c.W, i/c.W, color.NRGBA{uint8(p>>16&0xFF + bg), uint8(p>>8&0xFF + bg), uint8(p&0xFF + bg), 255})
		}
		f, _ := os.Create(out)
		_ = png.Encode(f, img)
		f.Close()
	}
}
