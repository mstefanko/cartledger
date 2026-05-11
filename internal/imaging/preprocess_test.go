package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"testing"
)

func TestPreprocessReceiptSkipsUnsafeTallCrop(t *testing.T) {
	raw := tallReceiptFixture(t, true)
	processed, err := PreprocessReceiptWithOptions(raw, testPreprocessOptions())
	if err != nil {
		t.Fatalf("PreprocessReceiptWithOptions: %v", err)
	}

	bounds := decodeBounds(t, processed)
	if bounds.Dy() < 700 {
		t.Fatalf("processed height = %d, want conservative full-height image", bounds.Dy())
	}
	if bounds.Dy() <= bounds.Dx() {
		t.Fatalf("processed bounds = %v, want portrait image", bounds)
	}
}

func TestPreprocessReceiptAllowsFullHeightSideCrop(t *testing.T) {
	raw := tallReceiptFixture(t, false)
	processed, err := PreprocessReceiptWithOptions(raw, testPreprocessOptions())
	if err != nil {
		t.Fatalf("PreprocessReceiptWithOptions: %v", err)
	}

	bounds := decodeBounds(t, processed)
	if bounds.Dy() < 760 {
		t.Fatalf("processed height = %d, want full-height crop", bounds.Dy())
	}
	if bounds.Dx() >= 560 {
		t.Fatalf("processed width = %d, want side borders cropped", bounds.Dx())
	}
}

func testPreprocessOptions() Options {
	opts := DefaultOptions()
	opts.MaxEdge = 800
	return opts
}

func tallReceiptFixture(t *testing.T, shadowedLowerHalf bool) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 1200, 1600))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: 55, G: 55, B: 55, A: 255}), image.Point{}, draw.Src)

	receipt := image.Rect(320, 0, 900, 1550)
	draw.Draw(img, receipt, image.NewUniform(color.RGBA{R: 238, G: 238, B: 232, A: 255}), image.Point{}, draw.Src)
	if shadowedLowerHalf {
		draw.Draw(img, image.Rect(receipt.Min.X, 520, receipt.Max.X, receipt.Max.Y), image.NewUniform(color.RGBA{R: 145, G: 145, B: 140, A: 255}), image.Point{}, draw.Src)
	}

	black := image.NewUniform(color.RGBA{A: 255})
	for y := 70; y < 1460; y += 42 {
		for x := 390; x < 780; x += 120 {
			draw.Draw(img, image.Rect(x, y, x+70, y+8), black, image.Point{}, draw.Src)
		}
		draw.Draw(img, image.Rect(790, y, 850, y+8), black, image.Point{}, draw.Src)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buf.Bytes()
}

func decodeBounds(t *testing.T, data []byte) image.Rectangle {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode processed image: %v", err)
	}
	return img.Bounds()
}
