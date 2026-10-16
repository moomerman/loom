package preview

import (
	"image"
	"image/color"
	"testing"
)

func fill(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestTrim(t *testing.T) {
	img := fill(100, 50, color.White)
	for y := 10; y < 20; y++ {
		for x := 30; x < 40; x++ {
			img.Set(x, y, color.Black)
		}
	}
	got := Trim(img, 4)
	if got.Bounds() != image.Rect(0, 0, 18, 18) {
		t.Errorf("trimmed to %v", got.Bounds())
	}
	if !same(got.At(0, 0), color.White) || !same(got.At(4, 4), color.Black) {
		t.Errorf("trim moved the content: %v at 0,0, %v at 4,4", got.At(0, 0), got.At(4, 4))
	}
	if b := Trim(fill(10, 10, color.White), 4).Bounds(); b.Dx() > 10 || b.Dy() > 10 {
		t.Errorf("blank image trimmed to %v", b)
	}
}

func same(a, b color.Color) bool { return sameColor(a, b) }
