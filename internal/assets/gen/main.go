// Command gen renders the application icon (a ringed planet on a deep-space
// disc) to internal/assets/icon.png and a small tray variant. Run with
// `go generate ./internal/assets`.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	// `go generate` runs inside internal/assets; plain `go run` from the root.
	root := "."
	if _, err := os.Stat("go.mod"); err != nil {
		root = "../.."
	}
	write(root+"/internal/assets/icon.png", render(512))
	write(root+"/internal/assets/tray.png", render(64))
	for _, sz := range []int{32, 48, 64, 128, 256, 512} {
		dir := fmt.Sprintf("%s/packaging/icons/hicolor/%dx%d/apps", root, sz, sz)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			panic(err)
		}
		write(dir+"/mu-archiver.png", render(sz))
	}
}

func write(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

type rgba = color.NRGBA

func lerp(a, b rgba, t float64) rgba {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return rgba{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: uint8(float64(a.A) + (float64(b.A)-float64(a.A))*t),
	}
}

func over(dst, src rgba) rgba {
	sa := float64(src.A) / 255
	da := float64(dst.A) / 255
	oa := sa + da*(1-sa)
	if oa == 0 {
		return rgba{}
	}
	ch := func(s, d uint8) uint8 {
		return uint8((float64(s)*sa + float64(d)*da*(1-sa)) / oa)
	}
	return rgba{R: ch(src.R, dst.R), G: ch(src.G, dst.G), B: ch(src.B, dst.B), A: uint8(oa * 255)}
}

// smoothstep-style edge: 1 inside, 0 outside, soft over `w` pixels.
func edge(d, w float64) float64 {
	if d <= -w {
		return 1
	}
	if d >= w {
		return 0
	}
	t := (d + w) / (2 * w)
	return 1 - t*t*(3-2*t)
}

func render(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	cx, cy := s/2, s/2
	ss := 3 // supersampling
	bgTop := rgba{R: 0x1b, G: 0x14, B: 0x3d, A: 255}
	bgBot := rgba{R: 0x0b, G: 0x08, B: 0x1f, A: 255}
	planetLight := rgba{R: 0xc4, G: 0xb5, B: 0xfd, A: 255}
	planetMid := rgba{R: 0x7c, G: 0x3a, B: 0xed, A: 255}
	planetDark := rgba{R: 0x3b, G: 0x0f, B: 0x8a, A: 255}
	ring := rgba{R: 0x5e, G: 0xea, B: 0xd4, A: 255}
	ringDim := rgba{R: 0x2d, G: 0xd4, B: 0xbf, A: 255}
	moon := rgba{R: 0xfd, G: 0xe6, B: 0x8a, A: 255}

	discR := s * 0.47
	planetR := s * 0.24
	aa := 1.0 / float64(ss)

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var acc [4]float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)*aa
					py := float64(y) + (float64(sy)+0.5)*aa
					c := sample(px, py, cx, cy, s, discR, planetR, bgTop, bgBot, planetLight, planetMid, planetDark, ring, ringDim, moon)
					acc[0] += float64(c.R) * float64(c.A)
					acc[1] += float64(c.G) * float64(c.A)
					acc[2] += float64(c.B) * float64(c.A)
					acc[3] += float64(c.A)
				}
			}
			n := float64(ss * ss)
			if acc[3] == 0 {
				img.SetNRGBA(x, y, rgba{})
				continue
			}
			img.SetNRGBA(x, y, rgba{R: uint8(acc[0] / acc[3]), G: uint8(acc[1] / acc[3]), B: uint8(acc[2] / acc[3]), A: uint8(acc[3] / n)})
		}
	}
	return img
}

func sample(px, py, cx, cy, s, discR, planetR float64, bgTop, bgBot, pl, pm, pd, ring, ringDim, moon rgba) rgba {
	dx, dy := px-cx, py-cy
	d := math.Hypot(dx, dy)
	var out rgba
	// background disc with vertical gradient and a subtle vignette
	bg := lerp(bgTop, bgBot, py/s)
	bg.A = uint8(255 * edge(d-discR, 1.0))
	out = over(out, bg)
	if out.A == 0 {
		return out
	}
	// a few faint stars
	for _, st := range [][3]float64{{0.22, 0.25, 0.012}, {0.78, 0.2, 0.009}, {0.3, 0.78, 0.008}, {0.7, 0.8, 0.011}, {0.82, 0.55, 0.007}, {0.18, 0.55, 0.006}} {
		sd := math.Hypot(px-st[0]*s, py-st[1]*s)
		if a := edge(sd-st[2]*s, 0.8); a > 0 {
			c := rgba{R: 0xe9, G: 0xe7, B: 0xf5, A: uint8(200 * a)}
			out = over(out, c)
		}
	}
	// ring (tilted ellipse), back half drawn before the planet
	rx, ry := planetR*1.85, planetR*0.55
	ang := -25 * math.Pi / 180
	ux := dx*math.Cos(ang) - dy*math.Sin(ang)
	uy := dx*math.Sin(ang) + dy*math.Cos(ang)
	e := math.Hypot(ux/rx, uy/ry)
	ringW := 0.11
	ringA := edge(math.Abs(e-1)-ringW, 0.02) // ~1 inside band
	back := uy < 0
	pdist := d - planetR
	if ringA > 0 && back {
		c := lerp(ringDim, ring, (ux/rx+1)/2)
		c.A = uint8(230 * ringA)
		out = over(out, c)
	}
	// planet: shaded sphere lit from the upper-left
	if a := edge(pdist, 1.0); a > 0 {
		nx, ny := dx/planetR, dy/planetR
		nz := math.Sqrt(math.Max(0, 1-nx*nx-ny*ny))
		lx, ly, lz := -0.55, -0.6, 0.58
		l := math.Max(0, nx*lx+ny*ly+nz*lz)
		var c rgba
		if l > 0.55 {
			c = lerp(pm, pl, (l-0.55)/0.45)
		} else {
			c = lerp(pd, pm, l/0.55)
		}
		c.A = uint8(255 * a)
		out = over(out, c)
	}
	// ring front half over the planet
	if ringA > 0 && !back {
		c := lerp(ringDim, ring, (ux/rx+1)/2)
		c.A = uint8(240 * ringA)
		out = over(out, c)
	}
	// small moon lower-right
	md := math.Hypot(px-(cx+planetR*1.35), py-(cy+planetR*1.15))
	if a := edge(md-planetR*0.17, 1.0); a > 0 {
		c := moon
		c.A = uint8(255 * a)
		out = over(out, c)
	}
	return out
}
