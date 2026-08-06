package main

import (
	"fmt"
	"image"
	_ "image/png"
	"math"
	"os"
)

func load(p string) image.Image {
	f, err := os.Open(p)
	if err != nil { panic(err) }
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil { panic(err) }
	return img
}

// overWhite composites a premultiplied RGBA sample over white, 0..255 range.
func overWhite(r, g, b, a uint32) (float64, float64, float64) {
	af := float64(a) / 65535
	return float64(r)/257 + 255*(1-af), float64(g)/257 + 255*(1-af), float64(b)/257 + 255*(1-af)
}

func main() {
	a, b := load(os.Args[1]), load(os.Args[2])
	ab, bb := a.Bounds(), b.Bounds()
	w, h := min(ab.Dx(), bb.Dx()), min(ab.Dy(), bb.Dy())
	var sum float64
	big := 0
	minX, minY, maxX, maxY := w, h, -1, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r1, g1, b1, a1 := a.At(ab.Min.X+x, ab.Min.Y+y).RGBA()
			r2, g2, b2, a2 := b.At(bb.Min.X+x, bb.Min.Y+y).RGBA()
			fr1, fg1, fb1 := overWhite(r1, g1, b1, a1)
			fr2, fg2, fb2 := overWhite(r2, g2, b2, a2)
			d := (math.Abs(fr1-fr2) + math.Abs(fg1-fg2) + math.Abs(fb1-fb2)) / 3
			sum += d
			if d > 64 {
				big++
				if x < minX { minX = x }
				if x > maxX { maxX = x }
				if y < minY { minY = y }
				if y > maxY { maxY = y }
			}
		}
	}
	n := float64(w * h)
	fmt.Printf("mean abs diff: %.2f/255\n", sum/n)
	fmt.Printf("pixels >64 diff: %.2f%%\n", float64(big)/n*100)
	if maxX >= 0 {
		fmt.Printf("big-diff bbox: x %d-%d  y %d-%d\n", minX, maxX, minY, maxY)
	}
}
