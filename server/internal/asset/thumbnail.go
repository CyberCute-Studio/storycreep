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

// decodeSlot lets one image at a time be decoded and scaled for a thumbnail
// in the process, whatever the number of jobs that run at once, so that the
// memory that a decoded image takes, up to 8 bytes for each of MaxPixels
// pixels, is taken once: a job holds the slot until it has scaled the image,
// after which only the thumbnail is left of it.
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
// which no decoder takes, does not. A JPEG image is turned and flipped as
// its EXIF orientation says, as a browser shows the file itself, so that the
// thumbnail of a photo that a phone took on its side is upright, as the
// photo is. It checks the image's size again before it decodes it, as an
// upload did, and waits for its turn to decode (decodeSlot) until ctx ends.
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
	dst, err := scaled(data, decode)
	<-decodeSlot
	if err != nil {
		return nil, err
	}
	if mime == MimeJPEG {
		dst = orient(dst, jpegOrientation(data))
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, dst); err != nil {
		return nil, fmt.Errorf("asset: encode the thumbnail: %w", err)
	}
	return out.Bytes(), nil
}

// scaled decodes data with decode and returns the image scaled to fit in
// ThumbnailSide × ThumbnailSide, keeping its proportions. The caller holds
// decodeSlot, which the decoded image is dropped within.
func scaled(data []byte, decode func([]byte) (image.Image, error)) (*image.NRGBA, error) {
	src, err := decode(data)
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
	return dst, nil
}

// orient returns img as the EXIF orientation o says to show it, which is how
// its pixels are stored: 1, as they are, 2 flipped left to right, 3 turned
// half a turn, 4 flipped top to bottom, 5 flipped along the diagonal from the
// top left, 6 turned a quarter clockwise, 7 flipped along the other diagonal,
// and 8 turned a quarter anticlockwise. It returns img itself for 1, and for
// a value that is no orientation.
func orient(img *image.NRGBA, o int) *image.NRGBA {
	if o < 2 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	ow, oh := w, h
	if o >= 5 {
		ow, oh = h, w
	}
	out := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := range oh {
		for x := range ow {
			// The pixel of img that is shown at x, y.
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			out.SetNRGBA(x, y, img.NRGBAAt(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return out
}
