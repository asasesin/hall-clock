// Generates the home-screen icons for the hall-clock web app:
//
//	go run ./scripts/icons src/hall-clock/web/assets
//
// The PNGs under web/assets are checked in; this exists so a colour or size
// change never means redrawing them by hand. Renders each icon with 8x
// supersampling from simple distance functions — deterministic, so a re-run
// with no edits reproduces the committed files byte for byte.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

type rgb struct{ r, g, b float64 }

func hex(c uint32) rgb {
	return rgb{
		float64((c >> 16) & 0xff),
		float64((c >> 8) & 0xff),
		float64(c & 0xff),
	}
}

func lerp(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

var (
	bgTop    = hex(0x182028)
	bgBottom = hex(0x0b0f13)
	white    = hex(0xf5f7fa)
	muted    = hex(0x95a0ad)
	green    = hex(0x44dc95)
	blue     = hex(0x6f9cff)
)

// distSeg is the distance from point p to segment a-b (all in unit space).
func distSeg(px, py, ax, ay, bx, by float64) float64 {
	vx, vy := bx-ax, by-ay
	wx, wy := px-ax, py-ay
	t := (wx*vx + wy*vy) / (vx*vx + vy*vy)
	t = math.Max(0, math.Min(1, t))
	dx, dy := px-(ax+vx*t), py-(ay+vy*t)
	return math.Hypot(dx, dy)
}

// shadeClock draws the control icon: a watch face at ten past ten.
func shadeClock(x, y float64) rgb {
	c := lerp(bgTop, bgBottom, y)
	cx, cy := x-0.5, y-0.5
	d := math.Hypot(cx, cy)

	const ringR = 0.30
	const ringW = 0.025

	// Hour ticks at 12/3/6/9.
	for i := 0; i < 4; i++ {
		ang := float64(i) * math.Pi / 2
		tx, ty := math.Sin(ang), -math.Cos(ang)
		if distSeg(cx, cy, tx*(ringR-0.075), ty*(ringR-0.075), tx*(ringR-0.045), ty*(ringR-0.045)) < 0.011 {
			return muted
		}
	}

	// Hands: hour toward 10, minute toward 2.
	hourAng := (10.0 + 10.0/60.0) / 12.0 * 2 * math.Pi
	minAng := 10.0 / 60.0 * 2 * math.Pi
	hx, hy := math.Sin(hourAng), -math.Cos(hourAng)
	mx, my := math.Sin(minAng), -math.Cos(minAng)
	if distSeg(cx, cy, 0, 0, hx*0.14, hy*0.14) < 0.020 {
		return green
	}
	if distSeg(cx, cy, 0, 0, mx*0.21, my*0.21) < 0.020 {
		return green
	}
	if d < 0.030 {
		return green
	}

	if math.Abs(d-ringR) < ringW {
		return white
	}
	return c
}

// shadeGear draws the setup icon: an eight-tooth gear.
func shadeGear(x, y float64) rgb {
	c := lerp(bgTop, bgBottom, y)
	cx, cy := x-0.5, y-0.5
	d := math.Hypot(cx, cy)
	ang := math.Atan2(cy, cx)

	const teeth = 8.0
	const body = 0.24
	const tooth = 0.32
	const hole = 0.10

	// Square-wave outer radius: teeth occupy 45% of each pitch.
	phase := math.Mod(ang*teeth/(2*math.Pi)+teeth+0.225, 1.0)
	outer := body
	if phase < 0.45 {
		outer = tooth
	}
	if d > hole && d < outer {
		return blue
	}
	if d <= hole && d > hole-0.035 {
		// Soften the hole rim with the panel tone so it reads at 48px.
		return lerp(bgTop, bgBottom, y)
	}
	return c
}

func render(size int, shade func(x, y float64) rgb) *image.RGBA {
	const ss = 8
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var acc rgb
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/ss) / float64(size)
					s := shade(x, y)
					acc.r += s.r
					acc.g += s.g
					acc.b += s.b
				}
			}
			n := float64(ss * ss)
			img.Set(px, py, color.RGBA{
				uint8(acc.r/n + 0.5),
				uint8(acc.g/n + 0.5),
				uint8(acc.b/n + 0.5),
				255,
			})
		}
	}
	return img
}

func main() {
	outDir := os.Args[1]
	icons := []struct {
		name  string
		shade func(x, y float64) rgb
	}{
		{"icon-control", shadeClock},
		{"icon-setup", shadeGear},
	}
	for _, ic := range icons {
		for _, size := range []int{180, 192, 512} {
			img := render(size, ic.shade)
			path := filepath.Join(outDir, fmt.Sprintf("%s-%d.png", ic.name, size))
			f, err := os.Create(path)
			if err != nil {
				panic(err)
			}
			if err := png.Encode(f, img); err != nil {
				panic(err)
			}
			f.Close()
			fmt.Println("wrote", path)
		}
	}
}
