package asset

import (
	"bytes"
	"encoding/binary"
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

// needsThumbnail reports whether img gets a thumbnail of its own, which its
// job makes: a raster image larger than ThumbnailSide on a side, unless its
// decoding would take more than maxDecodeBytes, as its header says
// (decodeBytes), which Thumbnail would refuse, so that its job need not
// read the whole file to learn so.
func needsThumbnail(img Image) bool {
	return img.Mime != MimeSVG && (img.Width > ThumbnailSide || img.Height > ThumbnailSide) &&
		decodeBytes(img) <= maxDecodeBytes
}

// decoders decode the raster types; a GIF's first frame.
var decoders = map[string]func([]byte) (image.Image, error){
	MimePNG:  func(b []byte) (image.Image, error) { return png.Decode(bytes.NewReader(b)) },
	MimeJPEG: func(b []byte) (image.Image, error) { return jpeg.Decode(bytes.NewReader(b)) },
	MimeGIF:  func(b []byte) (image.Image, error) { return gif.Decode(bytes.NewReader(b)) },
	MimeWebP: func(b []byte) (image.Image, error) { return webp.Decode(bytes.NewReader(b)) },
}

// maxDecodeBytes is the most memory that making a thumbnail may take, besides
// the file itself (decodeBytes): enough for an image of MaxPixels pixels in
// the encodings that photos and drawings are mostly saved in, a baseline
// JPEG, a progressive one whose colours are subsampled 4:2:0, a PNG of up to
// 16 bits a channel, or a lossy WebP without alpha, and for a lossless WebP,
// or one with alpha, of about 9 million pixels, and not for one in an
// encoding whose decoder takes two or three times as much for each pixel, as
// a progressive JPEG whose colours are not subsampled, or an interlaced PNG
// of 16 bits a channel, which is then its own thumbnail.
const maxDecodeBytes = 256 << 20

// Thumbnail returns the thumbnail of data, an image of the raster type mime,
// as a PNG that fits in ThumbnailSide × ThumbnailSide, keeping the image's
// proportions, or an error if data does not decode, as an animated WebP,
// which no decoder takes, does not. A JPEG image is turned and flipped as
// its EXIF orientation says, as a browser shows the file itself, so that the
// thumbnail of a photo that a phone took on its side is upright, as the
// photo is, and a GIF image is its first frame, where it is on the image's
// screen, as a browser shows it first. It checks the image's size again
// before it decodes it, as an upload did, and returns an error, without
// decoding it, for an image whose decoding would take more than
// maxDecodeBytes, as its header says (decodeBytes), so that a small file
// cannot make the server hold a huge image. A decoded image is held until it
// is scaled, after which only the thumbnail is left of it, so the job that
// calls Thumbnail makes one thumbnail at a time in a process (jobs.Serial),
// which bounds the memory that the thumbnails take.
func Thumbnail(data []byte, mime string) ([]byte, error) {
	decode, ok := decoders[mime]
	if !ok {
		return nil, fmt.Errorf("asset: no thumbnail is made of %s", mime)
	}
	img, err := Sniff(data, len(data))
	if err != nil {
		return nil, err
	}
	if img.Mime != mime {
		return nil, fmt.Errorf("asset: the file is %s, not %s", img.Mime, mime)
	}
	if n := decodeBytes(img); n > maxDecodeBytes {
		return nil, fmt.Errorf("asset: decoding the image would take %d MiB of memory, and a thumbnail may take %d MiB",
			n>>20, maxDecodeBytes>>20)
	}
	// A GIF's first frame may cover only part of the image's screen, which
	// the thumbnail shows whole, as Sniff read its size from the header.
	var screen image.Rectangle
	if mime == MimeGIF {
		screen = image.Rect(0, 0, img.Width, img.Height)
	}
	dst, err := scaled(data, decode, screen)
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

// decodeBytes returns about how many bytes of memory making the thumbnail of
// img, an image of a raster type that Sniff returned, takes, besides the file
// itself, as the decoders of the standard library and of golang.org/x/image
// allocate it: the decoded image, and what its decoder holds besides, which
// depend on how it is encoded as well as on its size, as its header says,
// and the buffer that scaling it to its thumbnail takes. A header that it
// cannot read is counted as the format's costliest encoding would be.
func decodeBytes(img Image) int64 {
	w, h := int64(img.Width), int64(img.Height)
	// Scaling takes 32 bytes for each pixel of the thumbnail's width for each
	// row of the image, which is as many for each pixel of the image's
	// shorter side as the thumbnail's longer side has pixels.
	n := 32 * ThumbnailSide * min(w, h)
	switch img.Mime {
	case MimeJPEG:
		b, ok := jpegDecodeBytes(img.Data)
		if !ok {
			// 4 components, each of 1 byte and of a coefficient of 4 bytes
			// for each pixel, and the 4 bytes of the CMYK image.
			b = 24 * w * h
		}
		return n + b
	case MimePNG:
		return n + pngDecodeBytes(img.Data, w*h)
	case MimeGIF:
		// The first frame's index of each pixel, and as many again for an
		// interlaced frame, as Pillow writes one by default, whose rows the
		// decoder copies into their order.
		return n + 2*w*h
	}
	// A WebP image, whose lossy decoder copies the frame's data out of the
	// file first.
	if !webpLossless(img.Data) {
		// A lossy image without alpha decodes to 1.5 bytes a pixel, of whole
		// macroblocks of 16 × 16 pixels.
		return n + 384*((w+15)/16)*((h+15)/16) + int64(len(img.Data))
	}
	// A lossless image decodes to 4 bytes a pixel, which a transform's output
	// takes as much again of, 8 in all, and a lossy one with alpha decodes
	// the alpha as a lossless image, of as many, which the garbage collector
	// may not have freed before the frame's own 1.5 bytes and the byte of the
	// copied-out alpha are held, so 11 in all; and the decoder of a lossless
	// image holds the Huffman codes that the file declares, which take up to
	// webpCodesBytes, whatever the image's size.
	return n + 11*w*h + webpCodesBytes + int64(len(img.Data))
}

// webpCodesBytes is the most memory that golang.org/x/image's decoder of a
// lossless WebP image holds of the Huffman codes that the file declares: up
// to 2,600 groups of 5 codes, which it refuses more of, each of a table of
// 512 bytes and a slice of 24, and of nodes of 8 bytes, 2 for each symbol of
// its alphabet, but one, of 2,328 symbols for the first code, 256 for each of
// the next three, and 40 for the last, so about 131 MiB, which a file of less
// than a megabyte can make it hold.
const webpCodesBytes = 2600 * (5*(512+24) + 8*(2*(2328+3*256+40)-5))

// webpLossless reports whether decoding the WebP file data reads a lossless
// image, whose decoder holds the Huffman codes that it declares: one whose
// image is lossless, a VP8L chunk, or whose VP8X chunk says that it has
// alpha, which a lossy image's ALPH chunk codes as a lossless image. The
// decoder reads the chunks in order up to the first that holds an image, as
// this does, so a file of a lossy image without alpha, a VP8 chunk, is the
// only one that is not, and a file whose chunks do not read up to an image
// counts as lossless.
func webpLossless(data []byte) bool {
	for i := 12; i+8 <= len(data); {
		size := int64(binary.LittleEndian.Uint32(data[i+4:]))
		switch string(data[i : i+4]) {
		case "VP8 ":
			return false
		case "VP8L":
			return true
		case "VP8X":
			const alpha = 1 << 4
			if i+9 > len(data) || data[i+8]&alpha != 0 {
				return true
			}
		}
		// A chunk's data is padded to an even length.
		next := int64(i) + 8 + size + size&1
		if next > int64(len(data)) {
			return true
		}
		i = int(next)
	}
	return true
}

// jpegDecodeBytes returns how many bytes image/jpeg allocates to decode the
// JPEG file data, as its frame's header says, and true, or false if no
// frame's header of a type that it decodes precedes the image data. Each of
// the image's components takes a byte for each of its samples, a
// progressive image's a coefficient of 4 bytes for each too, which the
// decoder keeps between its scans, and an image of 4 components, which is
// CMYK, or of 3 that are RGB rather than YCbCr, is converted to an image of 4
// bytes for each pixel. Whether 3 components are RGB the decoder decides once
// it has read the whole file, from the last JFIF or other APP0 segment that
// it read, and the last Adobe segment, which may come after the frame's
// header, or between the scans, so every segment of the file is read
// (jpegSegments), and the decoder's own rule followed.
func jpegDecodeBytes(data []byte) (int64, bool) {
	var jfif, adobeRGB, found, progressive bool
	var width, height, n int64
	var comps [4]struct {
		id   byte
		h, v int64
	}
	jpegSegments(data, func(marker byte, s []byte) bool {
		switch marker {
		case 0xe0:
			// Each APP0 segment of at least 5 bytes says anew whether the file
			// is JFIF, whose 3 components are YCbCr.
			if len(s) >= 5 {
				jfif = bytes.HasPrefix(s, []byte("JFIF\x00"))
			}
		case 0xee:
			// An Adobe segment whose transform is 0 says that 3 components
			// are RGB, unless a later Adobe segment says otherwise.
			if len(s) >= 12 && bytes.HasPrefix(s, []byte("Adobe")) {
				adobeRGB = s[11] == 0
			}
		case 0xc0, 0xc1, 0xc2:
			// The decoder refuses a file of more than one frame's header, so
			// only the first counts.
			if found {
				break
			}
			if len(s) < 6 {
				return false
			}
			height, width, n = int64(s[1])<<8|int64(s[2]), int64(s[3])<<8|int64(s[4]), int64(s[5])
			if n != 1 && n != 3 && n != 4 || int64(len(s)) < 6+3*n {
				return false
			}
			for i := range n {
				c := &comps[i]
				c.id, c.h, c.v = s[6+3*i], max(1, int64(s[7+3*i]>>4)), max(1, int64(s[7+3*i]&0x0f))
			}
			if n == 1 {
				// A single component is coded in blocks of 8 × 8, whatever
				// its sampling says.
				comps[0].h, comps[0].v = 1, 1
			}
			found, progressive = true, marker == 0xc2
		}
		return true
	})
	if !found {
		return 0, false
	}
	// The blocks of 8 × 8 samples of each component, in the minimum coded
	// units that cover the image, whose size the first component's sampling
	// gives.
	mx, my := (width+8*comps[0].h-1)/(8*comps[0].h), (height+8*comps[0].v-1)/(8*comps[0].v)
	var blocks int64
	for _, c := range comps[:n] {
		blocks += mx * my * c.h * c.v
	}
	perBlock := int64(64)
	if progressive {
		perBlock += 64 * 4
	}
	total := blocks * perBlock
	rgb := !jfif && (adobeRGB || comps[0].id == 'R' && comps[1].id == 'G' && comps[2].id == 'B')
	if n == 4 || n == 3 && rgb {
		total += 4 * width * height
	}
	return total, true
}

// pngDecodeBytes returns how many bytes image/png allocates to decode the PNG
// file data of the given number of pixels, as its header says: a byte for
// each pixel of a paletted image, 4 for one of up to 8 bits a channel, which
// is decoded as RGBA, as a grey image with a transparent colour is, and 8
// for one of 16 bits, and twice as much for an interlaced image, whose passes
// are decoded each into an image of its own before they are merged. A header
// that it cannot read is counted as an interlaced image of 16 bits a channel.
func pngDecodeBytes(data []byte, pixels int64) int64 {
	if len(data) < 29 || string(data[12:16]) != "IHDR" {
		return 16 * pixels
	}
	depth, colorType, interlaced := data[24], data[25], data[28] != 0
	perPixel := int64(4)
	switch {
	case colorType == 3:
		perPixel = 1
	case depth == 16:
		perPixel = 8
	}
	if interlaced {
		perPixel *= 2
	}
	return perPixel * pixels
}

// scaled decodes data with decode and returns the image scaled to fit in
// ThumbnailSide × ThumbnailSide, keeping its proportions. A screen that is
// not empty is a rectangle at the origin that holds the decoded image, as a
// GIF's screen holds its first frame: the thumbnail is then of the screen,
// with the decoded image scaled into its place on it, and the rest of it
// transparent.
func scaled(data []byte, decode func([]byte) (image.Image, error), screen image.Rectangle) (*image.NRGBA,
	error) {
	src, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("asset: decode the image: %w", err)
	}
	b := src.Bounds()
	if screen.Empty() {
		screen = b
	}
	sw, sh := screen.Dx(), screen.Dy()
	w, h := sw, sh
	if w > h {
		w, h = ThumbnailSide, max(1, h*ThumbnailSide/w)
	} else {
		w, h = max(1, w*ThumbnailSide/h), ThumbnailSide
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	// Where the decoded image is on the screen, scaled as the screen is.
	at := image.Rect((b.Min.X-screen.Min.X)*w/sw, (b.Min.Y-screen.Min.Y)*h/sh, (b.Max.X-screen.Min.X)*w/sw,
		(b.Max.Y-screen.Min.Y)*h/sh)
	draw.CatmullRom.Scale(dst, at, src, b, draw.Src, nil)
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
