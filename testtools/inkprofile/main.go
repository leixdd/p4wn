package main

// Structural comparison without exposing content: correlate per-row and
// per-column ink coverage between two renders. High correlation = same
// layout; low = misplaced/missing text.

import (
	"fmt"
	"image"
	_ "image/png"
	"math"
	"os"
)

func load(p string) image.Image {
	f, _ := os.Open(p)
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		panic(err)
	}
	return img
}

func ink(img image.Image) (rows, cols []float64) {
	b := img.Bounds()
	rows = make([]float64, b.Dy())
	cols = make([]float64, b.Dx())
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			af := float64(a) / 65535
			lum := (float64(r)+float64(g)+float64(bl))/3/65535*af + (1 - af)
			d := 1 - lum // darkness
			rows[y] += d
			cols[x] += d
		}
	}
	return
}

func corr(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sa, sb float64
	for i := 0; i < n; i++ {
		sa += a[i]
		sb += b[i]
	}
	ma, mb := sa/float64(n), sb/float64(n)
	var num, da, db float64
	for i := 0; i < n; i++ {
		x, y := a[i]-ma, b[i]-mb
		num += x * y
		da += x * x
		db += y * y
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

func main() {
	a, b := load(os.Args[1]), load(os.Args[2])
	ra, ca := ink(a)
	rb, cb := ink(b)
	// total ink ratio: are we drawing roughly as much text?
	var ta, tb float64
	for _, v := range ra {
		ta += v
	}
	for _, v := range rb {
		tb += v
	}
	fmt.Printf("row-profile corr: %.4f  col-profile corr: %.4f  ink ratio ours/ref: %.3f\n",
		corr(ra, rb), corr(ca, cb), ta/tb)
}
