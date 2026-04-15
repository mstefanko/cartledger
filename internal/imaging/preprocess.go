// Package imaging provides receipt image preprocessing to reduce file size
// and improve OCR quality before sending to the LLM vision API.
package imaging

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"log"

	// Register GIF decoder.
	_ "image/gif"

	"github.com/anthonynsimon/bild/adjust"
	"github.com/anthonynsimon/bild/effect"
	"github.com/anthonynsimon/bild/segment"
	"github.com/anthonynsimon/bild/transform"

	// Register WebP decoder (golang.org/x/image/webp).
	_ "golang.org/x/image/webp"
)

// Options configures the preprocessing pipeline.
type Options struct {
	MaxEdge       int     // Long edge max pixels (default 1568 — Claude's native limit)
	JPEGQuality   int     // Output JPEG quality (default 85)
	ContrastBoost float64 // Contrast adjustment -1.0 to 1.0 (default 0.2)
	CropThreshold uint8   // Binary threshold for border cropping (default 200)
	CropMinRatio  float64 // Skip crop if bounding box covers > this ratio (default 0.9)
}

// DefaultOptions returns sensible defaults for receipt preprocessing.
func DefaultOptions() Options {
	return Options{
		MaxEdge:       1568,
		JPEGQuality:   85,
		ContrastBoost: 0.2,
		CropThreshold: 200,
		CropMinRatio:  0.9,
	}
}

// PreprocessReceipt preprocesses a receipt image. On any failure, it returns
// the original raw bytes and a nil error — preprocessing must never block a scan.
func PreprocessReceipt(raw []byte) ([]byte, error) {
	processed, err := doPreprocess(raw, DefaultOptions())
	if err != nil {
		log.Printf("WARN: image preprocessing failed, using raw image: %v", err)
		return raw, nil
	}
	return processed, nil
}

// PreprocessReceiptWithOptions preprocesses with custom options. Same fallback behavior.
func PreprocessReceiptWithOptions(raw []byte, opts Options) ([]byte, error) {
	processed, err := doPreprocess(raw, opts)
	if err != nil {
		log.Printf("WARN: image preprocessing failed, using raw image: %v", err)
		return raw, nil
	}
	return processed, nil
}

func doPreprocess(raw []byte, opts Options) ([]byte, error) {
	// Step 1: Decode image (JPEG, PNG, GIF, WebP — detected from bytes).
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode image (%s): %w", format, err)
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Step 2: Resize — long edge to maxEdge pixels (Lanczos resampling).
	longEdge := w
	if h > w {
		longEdge = h
	}
	if longEdge > opts.MaxEdge {
		scale := float64(opts.MaxEdge) / float64(longEdge)
		newW := int(float64(w) * scale)
		newH := int(float64(h) * scale)
		img = transform.Resize(img, newW, newH, transform.Lanczos)
	}

	// Step 3: Grayscale.
	img = effect.Grayscale(img)

	// Step 4: Contrast boost.
	img = adjust.Contrast(img, opts.ContrastBoost)

	// Step 5: Sharpen — helps with thermal print text.
	img = effect.Sharpen(img)

	// Step 6: Border crop — trim dark background around receipt paper.
	img = cropBorders(img, opts.CropThreshold, opts.CropMinRatio)

	// Step 7: Re-encode as JPEG.
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: opts.JPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}

	return buf.Bytes(), nil
}

// cropBorders uses thresholding to find the receipt paper region and crops
// away dark borders. Skips if the bounding box covers most of the image.
func cropBorders(img image.Image, threshold uint8, minRatio float64) image.Image {
	// Binarize: pixels brighter than threshold → white, else black.
	binary := segment.Threshold(img, threshold)

	bounds := binary.Bounds()
	imgW, imgH := bounds.Dx(), bounds.Dy()

	// Find bounding box of white (paper) pixels.
	minX, minY := imgW, imgH
	maxX, maxY := 0, 0

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, _, _, _ := binary.At(x, y).RGBA()
			if r > 0 { // White pixel in binary image.
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}

	// No white pixels found — return original.
	if maxX <= minX || maxY <= minY {
		return img
	}

	boxW := maxX - minX
	boxH := maxY - minY
	coverageRatio := float64(boxW*boxH) / float64(imgW*imgH)

	// Skip crop if box covers most of the image (no clear background).
	if coverageRatio > minRatio {
		return img
	}

	// Add small padding (2% of each dimension).
	padX := imgW / 50
	padY := imgH / 50
	cropRect := image.Rect(
		max(minX-padX, bounds.Min.X),
		max(minY-padY, bounds.Min.Y),
		min(maxX+padX, bounds.Max.X),
		min(maxY+padY, bounds.Max.Y),
	)

	return transform.Crop(img, cropRect)
}

