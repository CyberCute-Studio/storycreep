package asset

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// ThumbnailSide is the side of the square that a thumbnail fits in (PRD
// FR-ASSET-02): a raster image larger than it on a side gets a thumbnail of
// its own, scaled to fit, as a PNG, which keeps its transparency, and a
// smaller one, or an SVG, which scales itself, is its own thumbnail.
const ThumbnailSide = 256

// decodeSlot lets one image at a time be decoded for a thumbnail in the
// process, whatever the number of jobs that run at once, so that the memory
// that decoding takes, up to 8 bytes for each of MaxPixels pixels, is taken
// once.
var decodeSlot = make(chan struct{}, 1)

// needsThumbnail reports whether img gets a thumbnail of its own.
func needsThumbnail(img Image) bool {
	return img.Mime != MimeSVG && (img.Width > ThumbnailSide || img.Height > ThumbnailSide)
}

// decoders decode the raster types; a GIF's first frame.
var decoders = map[string]func([]byte) (image.Image, error){
	MimePNG:  func(b []byte) (image.Image, error) { return png.Decode(bytes.NewReader(b)) },
	MimeJPEG: func(b []byte) (image.Image, error) { return jpeg.Decode(bytes.NewReader(b)) },
	MimeGIF:  func(b []byte) (image.Image, error) { return gif.Decode(bytes.NewReader(b)) },
	MimeWebP: func(b []byte) (image.Image, error) { return webp.Decode(bytes.NewReader(b)) },
}

// Thumbnail returns the thumbnail of data, an image of the raster type mime,
// as a PNG that fits in ThumbnailSide × ThumbnailSide, keeping the image's
// proportions, or an error if data does not decode, as an animated WebP,
// which no decoder takes, does not. It checks the image's size again before
// it decodes it, as an upload did, and waits for its turn to decode
// (decodeSlot) until ctx ends.
func Thumbnail(ctx context.Context, data []byte, mime string) ([]byte, error) {
	decode, ok := decoders[mime]
	if !ok {
		return nil, fmt.Errorf("asset: no thumbnail is made of %s", mime)
	}
	img, err := Sniff(data)
	if err != nil {
		return nil, err
	}
	if img.Mime != mime {
		return nil, fmt.Errorf("asset: the file is %s, not %s", img.Mime, mime)
	}
	select {
	case decodeSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	src, err := decode(data)
	<-decodeSlot
	if err != nil {
		return nil, fmt.Errorf("asset: decode the image: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > h {
		w, h = ThumbnailSide, max(1, h*ThumbnailSide/w)
	} else {
		w, h = max(1, w*ThumbnailSide/h), ThumbnailSide
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, dst); err != nil {
		return nil, fmt.Errorf("asset: encode the thumbnail: %w", err)
	}
	return out.Bytes(), nil
}
