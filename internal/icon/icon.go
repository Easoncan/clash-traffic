// Package icon draws the app's icons: an up arrow and a down arrow, for
// the traffic each way.
package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

type pt struct{ x, y float64 }

// The arrows in a 16×16 box.
var (
	upHead   = [3]pt{{1.2, 7.2}, {8.8, 7.2}, {5, 2}}
	upShaft  = [4]float64{4, 6.8, 6, 14} // x0, y0, x1, y1
	dnHead   = [3]pt{{7.2, 8.8}, {14.8, 8.8}, {11, 14}}
	dnShaft  = [4]float64{10, 2, 12, 9.2}
	samples  = 4
	boxUnits = 16.0
)

func inTriangle(p pt, t [3]pt) bool {
	sign := func(a, b, c pt) float64 { return (a.x-c.x)*(b.y-c.y) - (b.x-c.x)*(a.y-c.y) }
	d1, d2, d3 := sign(p, t[0], t[1]), sign(p, t[1], t[2]), sign(p, t[2], t[0])
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}

func inRect(p pt, r [4]float64) bool { return p.x >= r[0] && p.x <= r[2] && p.y >= r[1] && p.y <= r[3] }

// arrows reports whether a point of the 16×16 box is on an arrow: 1 for
// the up one, 2 for the down one.
func arrows(p pt) int {
	if inTriangle(p, upHead) || inRect(p, upShaft) {
		return 1
	}
	if inTriangle(p, dnHead) || inRect(p, dnShaft) {
		return 2
	}
	return 0
}

// coverage samples a pixel of an image size pixels wide on a grid,
// returning how much of it each shape covers.
func coverage(x, y, size int, box func(px, py float64) (pt, bool), shape func(pt) int) (cov [3]float64) {
	n := float64(samples * samples)
	for sy := range samples {
		for sx := range samples {
			px := float64(x) + (float64(sx)+0.5)/float64(samples)
			py := float64(y) + (float64(sy)+0.5)/float64(samples)
			if p, ok := box(px, py); ok {
				cov[shape(p)] += 1 / n
			}
		}
	}
	return cov
}

func encode(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// Tray returns the menu bar icon, black on transparent so that macOS tints
// it, size pixels square.
func Tray(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := float64(size) / boxUnits
	box := func(px, py float64) (pt, bool) { return pt{px / scale, py / scale}, true }
	for y := range size {
		for x := range size {
			c := coverage(x, y, size, box, arrows)
			a := c[1] + c[2]
			img.SetNRGBA(x, y, color.NRGBA{A: uint8(math.Round(a * 255))})
		}
	}
	return encode(img)
}

// App returns the application icon, size pixels square: the arrows on a
// rounded square with a gradient, with the margin macOS icons have.
func App(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	margin := s * 100 / 1024
	side := s - 2*margin
	radius := side * 0.225
	inSquare := func(px, py float64) bool {
		x, y := px-margin, py-margin
		if x < 0 || y < 0 || x > side || y > side {
			return false
		}
		cx := math.Min(math.Max(x, radius), side-radius)
		cy := math.Min(math.Max(y, radius), side-radius)
		return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= radius*radius
	}
	// The arrows fill the middle of the square.
	art := side * 0.62
	off := margin + (side-art)/2
	box := func(px, py float64) (pt, bool) {
		return pt{(px - off) / art * boxUnits, (py - off) / art * boxUnits}, true
	}
	top := [3]float64{0x4f, 0x46, 0xe5}    // indigo
	bottom := [3]float64{0x0e, 0xa5, 0xe9} // sky
	upCol := [3]float64{255, 255, 255}
	dnCol := [3]float64{0xd1, 0xfa, 0xe5}
	for y := range size {
		for x := range size {
			var sq float64
			for sy := range samples {
				for sx := range samples {
					if inSquare(float64(x)+(float64(sx)+0.5)/float64(samples), float64(y)+(float64(sy)+0.5)/float64(samples)) {
						sq++
					}
				}
			}
			sq /= float64(samples * samples)
			if sq == 0 {
				continue
			}
			t := (float64(y) - margin) / side
			t = math.Min(math.Max(t, 0), 1)
			var col [3]float64
			for i := range col {
				col[i] = top[i] + (bottom[i]-top[i])*t
			}
			c := coverage(x, y, size, box, arrows)
			for i := range col {
				col[i] = col[i]*(1-c[1]-c[2]) + upCol[i]*c[1] + dnCol[i]*c[2]
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(col[0]), uint8(col[1]), uint8(col[2]), uint8(math.Round(sq * 255))})
		}
	}
	return encode(img)
}
