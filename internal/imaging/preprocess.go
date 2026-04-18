// Package imaging provides receipt image preprocessing to reduce file size
// and improve OCR quality before sending to the LLM vision API.
package imaging

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"log/slog"

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
	CropThreshold uint8   // Binary threshold for border cropping (default 170)
	CropMinRatio  float64 // Skip crop if bounding box covers > this ratio (default 0.9)
}

// DefaultOptions returns sensible defaults for receipt preprocessing.
func DefaultOptions() Options {
	return Options{
		MaxEdge:       1568,
		JPEGQuality:   85,
		ContrastBoost: 0.2,
		CropThreshold: 170,
		CropMinRatio:  0.9,
	}
}

// StripMetadata re-encodes an image to drop all metadata (EXIF/GPS, XMP,
// IPTC, color profiles, etc.). This is important for privacy: phone photos
// commonly embed GPS coordinates, which would otherwise leak household
// members' locations to anyone who can view the receipt image.
//
// JPEG input is re-encoded as JPEG at the given quality (95 recommended for
// near-lossless originals). PNG input is re-encoded as PNG (lossless). GIF
// and WebP inputs are normalized to JPEG at the given quality.
//
// On any failure (unknown format, decode error, encode error), returns the
// error — callers must decide whether to abort the save or accept the raw
// (EXIF-carrying) bytes. For privacy-critical callers, treat the error as
// fatal and refuse to persist.
func StripMetadata(raw []byte, jpegQuality int) ([]byte, error) {
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode image (%s): %w", format, err)
	}

	var buf bytes.Buffer
	switch format {
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("encode png: %w", err)
		}
	default:
		// jpeg, gif, webp → jpeg.
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, fmt.Errorf("encode jpeg: %w", err)
		}
	}
	return buf.Bytes(), nil
}

// PreprocessReceipt preprocesses a receipt image. On any failure, it returns
// the original raw bytes and a nil error — preprocessing must never block a scan.
func PreprocessReceipt(raw []byte) ([]byte, error) {
	processed, err := doPreprocess(raw, DefaultOptions())
	if err != nil {
		slog.Warn("image preprocessing failed, using raw image", "err", err)
		return raw, nil
	}
	return processed, nil
}

// PreprocessReceiptWithOptions preprocesses with custom options. Same fallback behavior.
func PreprocessReceiptWithOptions(raw []byte, opts Options) ([]byte, error) {
	processed, err := doPreprocess(raw, opts)
	if err != nil {
		slog.Warn("image preprocessing failed, using raw image", "err", err)
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

	// Step 4: Border crop — run on clean grayscale BEFORE contrast/sharpen
	// to avoid bright artifacts fooling the threshold.
	img = cropBorders(img, opts.CropThreshold, opts.CropMinRatio)

	// Step 5: Contrast boost.
	img = adjust.Contrast(img, opts.ContrastBoost)

	// Step 6: Sharpen — helps with thermal print text.
	img = effect.Sharpen(img)

	// Step 7: Re-encode as JPEG.
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: opts.JPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}

	return buf.Bytes(), nil
}

// cropBorders uses thresholding to find the receipt paper region and crops
// away dark borders. Skips if the bounding box covers most of the image.
//
// Uses column/row projection: a column (or row) is considered "paper" only if
// at least 15% of its pixels are above the threshold. This filters out isolated
// bright specks from table reflections/wood grain.
func cropBorders(img image.Image, threshold uint8, minRatio float64) image.Image {
	// Binarize: pixels brighter than threshold → white, else black.
	binary := segment.Threshold(img, threshold)

	bounds := binary.Bounds()
	imgW, imgH := bounds.Dx(), bounds.Dy()
	minDensity := 0.15 // 15% of pixels in a row/col must be bright to count

	// Column projection: for each x, count bright pixels.
	colMinX, colMaxX := imgW, 0
	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		bright := 0
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			r, _, _, _ := binary.At(x, y).RGBA()
			if r > 0 {
				bright++
			}
		}
		if float64(bright)/float64(imgH) >= minDensity {
			if x < colMinX {
				colMinX = x
			}
			if x > colMaxX {
				colMaxX = x
			}
		}
	}

	// Row projection: for each y, count bright pixels.
	rowMinY, rowMaxY := imgH, 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		bright := 0
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, _, _, _ := binary.At(x, y).RGBA()
			if r > 0 {
				bright++
			}
		}
		if float64(bright)/float64(imgW) >= minDensity {
			if y < rowMinY {
				rowMinY = y
			}
			if y > rowMaxY {
				rowMaxY = y
			}
		}
	}

	// No paper region found — return original.
	if colMaxX <= colMinX || rowMaxY <= rowMinY {
		return img
	}

	boxW := colMaxX - colMinX
	boxH := rowMaxY - rowMinY
	coverageRatio := float64(boxW*boxH) / float64(imgW*imgH)

	// Skip crop if box covers most of the image (no clear background).
	if coverageRatio > minRatio {
		return img
	}

	// Add small padding (2% of each dimension).
	padX := imgW / 50
	padY := imgH / 50
	cropRect := image.Rect(
		max(colMinX-padX, bounds.Min.X),
		max(rowMinY-padY, bounds.Min.Y),
		min(colMaxX+padX, bounds.Max.X),
		min(rowMaxY+padY, bounds.Max.Y),
	)

	return transform.Crop(img, cropRect)
}

