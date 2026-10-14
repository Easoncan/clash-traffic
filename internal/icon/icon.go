// Package icon draws the app's icons: a cat's head, as Clash's, whose
// eyes are an up arrow and a down arrow, for the traffic each way.
package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

type vec struct{ x, y float64 }

func (a vec) sub(b vec) vec       { return vec{a.x - b.x, a.y - b.y} }
func (a vec) dot(b vec) float64   { return a.x*b.x + a.y*b.y }
func (a vec) scale(k float64) vec { return vec{a.x * k, a.y * k} }
func (a vec) len() float64        { return math.Hypot(a.x, a.y) }

// sdPolygon is the signed distance to a convex polygon, its corners in
// either order: negative inside.
func sdPolygon(p vec, pts ...vec) float64 {
	d := math.Inf(1)
	pos, neg := false, false
	for i := range pts {
		a, b := pts[i], pts[(i+1)%len(pts)]
		e, w := b.sub(a), p.sub(a)
		t := math.Max(0, math.Min(1, w.dot(e)/e.dot(e)))
		d = math.Min(d, w.sub(e.scale(t)).len())
		if cross := e.x*w.y - e.y*w.x; cross > 0 {
			pos = true
		} else if cross < 0 {
			neg = true
		}
	}
	if !(pos && neg) {
		return -d
	}
	return d
}

// rounded insets a convex polygon by r about its centroid, so that the
// distance to it less r is the polygon with corners rounded by r.
func rounded(r float64, pts ...vec) []vec {
	var c vec
	for _, p := range pts {
		c.x += p.x / float64(len(pts))
		c.y += p.y / float64(len(pts))
	}
	out := make([]vec, len(pts))
	for i, p := range pts {
		d := p.sub(c)
		out[i] = c.sub(d.scale(-(d.len() - 1.6*r) / d.len()))
	}
	return out
}

// sdEllipse approximates the signed distance to an ellipse.
func sdEllipse(p, c, r vec) float64 {
	q := p.sub(c)
	k0 := math.Hypot(q.x/r.x, q.y/r.y)
	k1 := math.Hypot(q.x/(r.x*r.x), q.y/(r.y*r.y))
	if k1 == 0 {
		return -math.Min(r.x, r.y)
	}
	return k0 * (k0 - 1) / k1
}

// sdRoundRect is the signed distance to a rectangle with corners rounded
// by r.
func sdRoundRect(p, c, half vec, r float64) float64 {
	q := vec{math.Abs(p.x-c.x) - half.x + r, math.Abs(p.y-c.y) - half.y + r}
	out := vec{math.Max(q.x, 0), math.Max(q.y, 0)}.len()
	return out + math.Min(math.Max(q.x, q.y), 0) - r
}

// catHead is the signed distance to the cat's head, ears and all, in a
// unit box.
func catHead(p vec) float64 {
	head := sdEllipse(p, vec{0.5, 0.6}, vec{0.35, 0.28})
	ear := 0.035
	left := sdPolygon(p, rounded(ear, vec{0.16, 0.53}, vec{0.47, 0.35}, vec{0.21, 0.12})...) - ear
	right := sdPolygon(p, rounded(ear, vec{0.84, 0.53}, vec{0.79, 0.12}, vec{0.53, 0.35})...) - ear
	return math.Min(head, math.Min(left, right))
}

// arrow is the signed distance to an arrow at c pointing up (dir -1) or
// down (dir 1), h tall, its head w wide and its shaft shaftW.
func arrow(p, c vec, h, w, shaftW, dir float64) float64 {
	headH := h * 0.5
	tip := vec{c.x, c.y + dir*h/2}
	base := c.y + dir*(h/2-headH)
	head := sdPolygon(p, vec{c.x - w/2, base}, tip, vec{c.x + w/2, base})
	shaft := sdRoundRect(p, vec{c.x, c.y - dir*headH/2 + dir*0.001}, vec{shaftW / 2, (h-headH)/2 + 0.01}, shaftW*0.3)
	return math.Min(head, shaft)
}

func eyes(p vec) float64 {
	const h = 0.155
	up := arrow(p, vec{0.375, 0.6}, h, h*0.78, h*0.26, -1)
	down := arrow(p, vec{0.625, 0.6}, h, h*0.78, h*0.26, 1)
	return math.Min(up, down)
}

func nose(p vec) float64 {
	r := 0.012
	return sdPolygon(p, rounded(r, vec{0.465, 0.715}, vec{0.535, 0.715}, vec{0.5, 0.755})...) - r
}

// coverage is how much of the pixel at (x, y) of a size-pixel image the
// shape covers, sampled on a grid.
func coverage(x, y, size int, inside func(vec) bool) float64 {
	const n = 4
	hits := 0
	for sy := range n {
		for sx := range n {
			p := vec{(float64(x) + (float64(sx)+0.5)/n) / float64(size), (float64(y) + (float64(sy)+0.5)/n) / float64(size)}
			if inside(p) {
				hits++
			}
		}
	}
	return float64(hits) / (n * n)
}

func encode(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// trayCat is the cat drawn for the menu bar, in a unit box: broader and
// squatter than the app's, with eyes bold enough to read at 16 points.
func trayCat(p vec) float64 {
	head := sdEllipse(p, vec{0.5, 0.6}, vec{0.46, 0.34})
	ear := 0.05
	left := sdPolygon(p, rounded(ear, vec{0.08, 0.5}, vec{0.46, 0.34}, vec{0.14, 0.07})...) - ear
	right := sdPolygon(p, rounded(ear, vec{0.92, 0.5}, vec{0.86, 0.07}, vec{0.54, 0.34})...) - ear
	cat := math.Min(head, math.Min(left, right))
	const h, w, shaftW = 0.33, 0.26, 0.1
	up := arrow(p, vec{0.32, 0.62}, h, w, shaftW, -1)
	down := arrow(p, vec{0.68, 0.62}, h, w, shaftW, 1)
	return math.Max(cat, -math.Min(up, down))
}

// Tray returns the menu bar icon, black on transparent so that macOS tints
// it, size pixels square: the cat, its eyes cut out.
func Tray(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	inside := func(p vec) bool { return trayCat(p) <= 0 }
	for y := range size {
		for x := range size {
			a := coverage(x, y, size, inside)
			img.SetNRGBA(x, y, color.NRGBA{A: uint8(math.Round(a * 255))})
		}
	}
	return encode(img)
}

// The gradient of the icon's square: pink, as Clash Verge's, to violet to
// the blue the app draws downloads in.
var stops = []struct {
	at  float64
	rgb [3]float64
}{
	{0, [3]float64{0xff, 0x7e, 0xb3}},
	{0.5, [3]float64{0x9b, 0x6b, 0xf2}},
	{1, [3]float64{0x3f, 0x82, 0xf0}},
}

func gradient(t float64) [3]float64 {
	t = math.Max(0, math.Min(1, t))
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].at {
			a, b := stops[i-1], stops[i]
			k := (t - a.at) / (b.at - a.at)
			return [3]float64{a.rgb[0] + (b.rgb[0]-a.rgb[0])*k, a.rgb[1] + (b.rgb[1]-a.rgb[1])*k, a.rgb[2] + (b.rgb[2]-a.rgb[2])*k}
		}
	}
	return stops[len(stops)-1].rgb
}

// App returns the application icon, size pixels square: the cat on a
// rounded square of the gradient, with the margin macOS icons have.
func App(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const margin = 100.0 / 1024
	side := 1 - 2*margin
	square := func(p vec) bool {
		return sdRoundRect(p, vec{0.5, 0.5}, vec{side / 2, side / 2}, side*0.225) <= 0
	}
	// The cat takes the middle of the square.
	art, top := side*0.7, 0.5-side*0.7/2-side*0.02
	toCat := func(p vec) vec { return vec{(p.x-0.5)/art + 0.5, (p.y - top) / art} }
	white := func(p vec) bool {
		q := toCat(p)
		return catHead(q) <= 0 && eyes(q) > 0 && nose(q) > 0
	}
	for y := range size {
		for x := range size {
			sq := coverage(x, y, size, square)
			if sq == 0 {
				continue
			}
			p := vec{(float64(x) + 0.5) / float64(size), (float64(y) + 0.5) / float64(size)}
			col := gradient((p.x-margin)/side*0.35 + (p.y-margin)/side*0.65)
			w := coverage(x, y, size, white)
			for i := range col {
				col[i] = col[i]*(1-w) + 255*w
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(col[0]), uint8(col[1]), uint8(col[2]), uint8(math.Round(sq * 255))})
		}
	}
	return encode(img)
}
