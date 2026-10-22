package asset

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand/v2"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// rgba returns an image of width × height pixels.
func rgba(width, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 9, A: 255})
		}
	}
	return img
}

func encoded(t *testing.T, encode func(*bytes.Buffer, image.Image) error, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// testLimit is the most bytes that the tests' SVG images may take once they
// are sanitized: the most that an instance's uploads may.
const testLimit = 50 << 20

// minimalWebP is a lossless WebP image of 1 × 1 pixel.
var minimalWebP = []byte("RIFF\x1a\x00\x00\x00WEBPVP8L\x0d\x00\x00\x00\x2f\x00\x00\x00\x10\x07\x10\x11\x11\x88\x88\xfe\x07\x00")

// TestSniff checks that the type of a file comes from its content, and its
// size from its header, and that a file that is none of the types, or whose
// header does not decode, or that is too large, is refused with the reason.
//
// Verifies: FR-ASSET-02/AC1
func TestSniff(t *testing.T) {
	img := rgba(30, 20)
	for _, tt := range []struct {
		name, mime, ext string
		data            []byte
	}{
		{"PNG", MimePNG, "png", encoded(t, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) }, img)},
		{"JPEG", MimeJPEG, "jpg", encoded(t, func(b *bytes.Buffer, i image.Image) error {
			return jpeg.Encode(b, i, nil)
		}, img)},
		{"GIF", MimeGIF, "gif", encoded(t, func(b *bytes.Buffer, i image.Image) error {
			return gif.Encode(b, i, nil)
		}, img)},
	} {
		got, err := Sniff(tt.data, testLimit)
		if err != nil || got.Mime != tt.mime || got.Ext != tt.ext || got.Width != 30 || got.Height != 20 ||
			!bytes.Equal(got.Data, tt.data) {
			t.Errorf("%s: %+v, %v", tt.name, got, err)
		}
	}
	if got, err := Sniff(minimalWebP, testLimit); err != nil || got.Mime != MimeWebP || got.Width != 1 || got.Height != 1 {
		t.Errorf("WebP: %+v, %v", got, err)
	}
	// A JPEG photo that its EXIF data says to turn a quarter is as large as
	// it is shown; one turned half a turn is as large as it is stored.
	jpg := encoded(t, func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) }, img)
	for _, tt := range []struct {
		orientation, w, h int
		big               bool
	}{{6, 20, 30, true}, {8, 20, 30, false}, {5, 20, 30, true}, {3, 30, 20, false}, {1, 30, 20, true}} {
		got, err := Sniff(withOrientation(jpg, tt.orientation, tt.big), testLimit)
		if err != nil || got.Width != tt.w || got.Height != tt.h {
			t.Errorf("a JPEG of the orientation %d: %d × %d, %v; want %d × %d", tt.orientation, got.Width,
				got.Height, err, tt.w, tt.h)
		}
	}

	for _, tt := range []struct {
		name   string
		data   []byte
		reason string
	}{
		{"empty", nil, ReasonType},
		{"text", []byte("hello, image"), ReasonType},
		{"a ZIP", []byte("PK\x03\x04 rest"), ReasonType},
		{"a RIFF of another form", []byte("RIFF\x04\x00\x00\x00WAVEfmt "), ReasonType},
		{"a PNG signature alone", []byte("\x89PNG\r\n\x1a\n"), ReasonMalformed},
		{"a JPEG signature alone", []byte("\xff\xd8\xff"), ReasonMalformed},
		{"a WebP header alone", []byte("RIFF\x04\x00\x00\x00WEBP"), ReasonMalformed},
		{"a PNG of no pixels", pngOf(0, 5), ReasonMalformed},
		{"a PNG of too many pixels", pngOf(6000, 5000), ReasonDimensions},
		{"a PNG too tall", pngOf(1, MaxSide+1), ReasonDimensions},
	} {
		_, err := Sniff(tt.data, testLimit)
		var ie *ImageError
		if !errors.As(err, &ie) || ie.Reason != tt.reason {
			t.Errorf("%s: %v, want an ImageError of %s", tt.name, err, tt.reason)
		}
	}
	if _, err := Sniff(pngOf(5000, 5000), testLimit); err != nil {
		t.Errorf("a PNG of %d pixels, the most: %v", MaxPixels, err)
	}
}

// withOrientation returns the JPEG file jpg with an APP1 segment of EXIF data
// after its start, which says the orientation o, in a TIFF header of big
// endian byte order if big is set, and of little endian otherwise.
func withOrientation(jpg []byte, o int, big bool) []byte {
	var order binary.AppendByteOrder = binary.LittleEndian
	tiff := []byte("II")
	if big {
		order, tiff = binary.BigEndian, []byte("MM")
	}
	tiff = order.AppendUint16(tiff, 42)
	tiff = order.AppendUint32(tiff, 8)
	// An image directory of two entries: another tag first, and then the
	// orientation, a SHORT, of which there is one.
	tiff = order.AppendUint16(tiff, 2)
	tiff = order.AppendUint16(tiff, 0x010f)
	tiff = order.AppendUint16(tiff, 2)
	tiff = order.AppendUint32(tiff, 4)
	tiff = append(tiff, 'c', 'a', 'm', 0)
	tiff = order.AppendUint16(tiff, 0x0112)
	tiff = order.AppendUint16(tiff, 3)
	tiff = order.AppendUint32(tiff, 1)
	tiff = order.AppendUint16(tiff, uint16(o))
	tiff = append(tiff, 0, 0, 0, 0, 0, 0)
	segment := append([]byte("Exif\x00\x00"), tiff...)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, 0xff, 0xe1)
	out = binary.BigEndian.AppendUint16(out, uint16(len(segment)+2))
	out = append(out, segment...)
	return append(out, jpg[2:]...)
}

// TestJPEGOrientation checks that the orientation is read from EXIF data
// alone, and is 1 for a file without any, or whose data is not valid.
func TestJPEGOrientation(t *testing.T) {
	jpg := encoded(t, func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) }, rgba(4, 4))
	if o := jpegOrientation(jpg); o != 1 {
		t.Errorf("a JPEG without EXIF data: %d, want 1", o)
	}
	for o := 1; o <= 8; o++ {
		for _, big := range []bool{true, false} {
			if got := jpegOrientation(withOrientation(jpg, o, big)); got != o {
				t.Errorf("the orientation %d, big endian %v: %d", o, big, got)
			}
		}
	}
	bad := withOrientation(jpg, 9, true)
	if o := jpegOrientation(bad); o != 1 {
		t.Errorf("the orientation 9: %d, want 1", o)
	}
	// A stray byte between the start and the EXIF segment, which image/jpeg
	// skips, does not hide the orientation.
	junk := slices.Insert(withOrientation(jpg, 6, true), 2, byte(0))
	if o := jpegOrientation(junk); o != 6 {
		t.Errorf("a stray byte before the EXIF segment: %d, want 6", o)
	}
	// EXIF data after the first scan is not read, as decoders read the
	// segments before it alone.
	exif := withOrientation([]byte{0xff, 0xd8}, 6, true)[2:]
	if o := jpegOrientation(jpegFile(nil, nil, exif, 0xc0, 8, 8, [2]byte{1, 0x11})); o != 1 {
		t.Errorf("EXIF data after the scan: %d, want 1", o)
	}
	cut := withOrientation(jpg, 6, true)[:30]
	if o := jpegOrientation(cut); o != 1 {
		t.Errorf("EXIF data cut short: %d, want 1", o)
	}
	for _, data := range [][]byte{nil, []byte("\xff\xd8"), []byte("\xff\xd8\xff\xe1\x00\x01"),
		[]byte("\xff\xd8\xff\xe1\xff\xffExif\x00\x00")} {
		if o := jpegOrientation(data); o != 1 {
			t.Errorf("%q: %d, want 1", data, o)
		}
	}
}

// pngOf returns a PNG file of a header that says width × height, of RGBA
// of 8 bits a channel, and nothing else.
func pngOf(width, height int) []byte {
	return pngWith(width, height, 8, 6, false)
}

// pngWith returns a PNG file of a header that says width × height, of the
// depth and the colour type, interlaced if interlaced is set, and nothing
// else, but a palette of one colour and an empty data chunk if the type is
// paletted.
func pngWith(width, height int, depth, colorType byte, interlaced bool) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	interlace := byte(0)
	if interlaced {
		interlace = 1
	}
	ihdr := []byte{0, 0, 0, 13, 'I', 'H', 'D', 'R',
		byte(width >> 24), byte(width >> 16), byte(width >> 8), byte(width),
		byte(height >> 24), byte(height >> 16), byte(height >> 8), byte(height), depth, colorType, 0, 0, interlace}
	b.Write(ihdr)
	sum := crc(ihdr[4:])
	b.Write([]byte{byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum)})
	if colorType == 3 {
		// The header of a paletted image decodes at its first data chunk.
		for _, chunk := range [][]byte{{0, 0, 0, 3, 'P', 'L', 'T', 'E', 9, 9, 9}, {0, 0, 0, 0, 'I', 'D', 'A', 'T'}} {
			b.Write(chunk)
			sum := crc(chunk[4:])
			b.Write([]byte{byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum)})
		}
	}
	return b.Bytes()
}

// crc is PNG's checksum of a chunk's type and data.
func crc(b []byte) uint32 {
	c := ^uint32(0)
	for _, x := range b {
		c ^= uint32(x)
		for range 8 {
			if c&1 != 0 {
				c = c>>1 ^ 0xedb88320
			} else {
				c >>= 1
			}
		}
	}
	return ^c
}

// TestThumbnail checks that a thumbnail fits in ThumbnailSide on its longer
// side, keeps the image's proportions, and is a PNG, and that an image that
// does not decode, or whose type is not what the asset says, has none.
func TestThumbnail(t *testing.T) {
	for _, tt := range []struct {
		width, height, wantW, wantH int
	}{
		{1024, 512, 256, 128}, {300, 900, 85, 256}, {3000, 2, 256, 1},
	} {
		data := encoded(t, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) },
			rgba(tt.width, tt.height))
		thumb, err := Thumbnail(data, MimePNG)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(thumb))
		if err != nil || cfg.Width != tt.wantW || cfg.Height != tt.wantH {
			t.Errorf("%d × %d: a thumbnail of %d × %d, %v; want %d × %d", tt.width, tt.height, cfg.Width,
				cfg.Height, err, tt.wantW, tt.wantH)
		}
	}
	jpg := encoded(t, func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) }, rgba(400, 400))
	if _, err := Thumbnail(jpg, MimeJPEG); err != nil {
		t.Errorf("a JPEG: %v", err)
	}
	if _, err := Thumbnail(jpg, MimePNG); err == nil {
		t.Error("a JPEG said to be a PNG has a thumbnail")
	}
	truncated := jpg[:len(jpg)/2]
	if _, err := Thumbnail(truncated, MimeJPEG); err == nil {
		t.Error("a truncated JPEG has a thumbnail")
	}
	if _, err := Thumbnail([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), MimeSVG); err == nil ||
		!strings.Contains(err.Error(), "no thumbnail") {
		t.Errorf("an SVG: %v", err)
	}

	// A photo stored on its side, red on the left and blue on the right,
	// that its EXIF data says to turn a quarter clockwise, has a thumbnail
	// that is upright, as a browser shows the photo: red at the top.
	side := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	for y := range 200 {
		for x := range 400 {
			c := color.NRGBA{R: 255, A: 255}
			if x >= 200 {
				c = color.NRGBA{B: 255, A: 255}
			}
			side.SetNRGBA(x, y, c)
		}
	}
	photo := withOrientation(encoded(t, func(b *bytes.Buffer, i image.Image) error {
		return jpeg.Encode(b, i, &jpeg.Options{Quality: 95})
	}, side), 6, true)
	thumb, err := Thumbnail(photo, MimeJPEG)
	if err != nil {
		t.Fatal(err)
	}
	upright, err := png.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatal(err)
	}
	if b := upright.Bounds(); b.Dx() != 128 || b.Dy() != 256 {
		t.Fatalf("the thumbnail of a photo turned a quarter is %d × %d, want 128 × 256", b.Dx(), b.Dy())
	}
	top, bottom := color.NRGBAModel.Convert(upright.At(64, 20)).(color.NRGBA),
		color.NRGBAModel.Convert(upright.At(64, 236)).(color.NRGBA)
	if top.R < 200 || top.B > 60 || bottom.B < 200 || bottom.R > 60 {
		t.Errorf("the thumbnail is %v at the top and %v at the bottom, want red and blue", top, bottom)
	}
}

// The segments that say how a JPEG file's components are coded: JFIF, whose
// 3 components are YCbCr, and Adobe's, whose transform 0 makes them RGB.
var (
	jfifSegment     = []byte("\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")
	adobeRGBSegment = []byte("\xff\xee\x00\x0eAdobe\x00\x64\x00\x00\x00\x00\x00")
	// jfxxSegment is an APP0 segment of a JFIF extension, which says anew that
	// the file is not JFIF.
	jfxxSegment = []byte("\xff\xe0\x00\x08JFXX\x00\x10")
)

// jpegHeader returns a JPEG file of the segments app, and then a frame's
// header of the marker sof, of width × height pixels, whose components have
// the identifiers and the samplings of comps, and a scan's header, at which
// the header decodes, and nothing else.
func jpegHeader(app []byte, sof byte, width, height int, comps ...[2]byte) []byte {
	b := append([]byte{0xff, 0xd8}, app...)
	b = append(b, 0xff, sof, 0, byte(8+3*len(comps)), 8, byte(height>>8), byte(height), byte(width>>8), byte(width),
		byte(len(comps)))
	for _, c := range comps {
		b = append(b, c[0], c[1], 0)
	}
	return append(b, 0xff, 0xda, 0, 8, 1, comps[0][0], 0, 0, 63, 0)
}

// jpegFile returns a JPEG file of the segments before, a frame's header of
// the marker sof, of width × height pixels, whose components have the
// identifiers and the samplings of comps, the segments afterFrame, a scan's
// header and entropy-coded data, with an escaped 0xff and a restart marker,
// the segments afterScan, and the end of the image.
func jpegFile(before, afterFrame, afterScan []byte, sof byte, width, height int, comps ...[2]byte) []byte {
	b := jpegHeader(before, sof, width, height, comps...)
	// jpegHeader ends with a scan's header of 10 bytes.
	scan := slices.Clone(b[len(b)-10:])
	b = append(b[:len(b)-10], afterFrame...)
	b = append(b, scan...)
	b = append(b, 0x12, 0x34, 0xff, 0x00, 0x56, 0xff, 0xd0, 0x78)
	b = append(b, afterScan...)
	return append(b, 0xff, 0xd9)
}

// webpOf returns a WebP file of the chunks, each a FourCC and its data.
func webpOf(chunks ...string) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBP")
	for i := 0; i+1 < len(chunks); i += 2 {
		b = append(b, chunks[i]...)
		b = binary.LittleEndian.AppendUint32(b, uint32(len(chunks[i+1])))
		b = append(b, chunks[i+1]...)
		if len(chunks[i+1])%2 == 1 {
			b = append(b, 0)
		}
	}
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	return b
}

// TestDecodeBytes checks the memory that making a thumbnail takes, as an
// image's header says, against what the decoders allocate for each encoding,
// and that Thumbnail decodes no image that would take more than
// maxDecodeBytes, while a photo of nearly MaxPixels pixels, saved as a
// progressive JPEG whose colours are subsampled, takes less.
func TestDecodeBytes(t *testing.T) {
	const side = 5000
	// Scaling an image of side × side pixels to its thumbnail.
	scale := int64(32 * ThumbnailSide * side)
	all := [2]byte{1, 0x11}
	cmyk := jpegHeader(adobeRGBSegment, 0xc2, side, side, all, [2]byte{2, 0x11}, [2]byte{3, 0x11}, [2]byte{4, 0x11})
	rgb := [][2]byte{{'R', 0x11}, {'G', 0x11}, {'B', 0x11}}
	// 313 × 313 units of 4 blocks of luma and 2 of chroma, each of 64 samples
	// and 64 coefficients of 4 bytes.
	sub := [][2]byte{{1, 0x22}, {2, 0x11}, {3, 0x11}}
	rgbSub := [][2]byte{{'R', 0x22}, {'G', 0x11}, {'B', 0x11}}
	const sub420 = 313 * 313 * 6 * (64 + 256)
	// side - 1 in 14 bits, twice, as VP8L's and VP8's headers say it.
	lossy := webpOf("VP8 ", "\x10\x00\x00\x9d\x01\x2a\x88\x13\x88\x13")
	lossless := webpOf("VP8L", "\x2f\x87\xd3\xe1\x04")
	alpha := webpOf("VP8X", "\x10\x00\x00\x00\x87\x13\x00\x87\x13\x00", "ALPH", "\x00", "VP8 ",
		"\x10\x00\x00\x9d\x01\x2a\x88\x13\x88\x13")
	for _, tt := range []struct {
		name string
		mime string
		data []byte
		want int64
	}{
		// 625 × 625 blocks of each of 4 components, of 64 samples and 64
		// coefficients of 4 bytes, and the CMYK image.
		{"a progressive CMYK JPEG", MimeJPEG, cmyk, 4*625*625*(64+256) + 4*side*side + scale},
		{"a baseline YCbCr JPEG", MimeJPEG, jpegHeader(jfifSegment, 0xc0, side, side, all, [2]byte{2, 0x11},
			[2]byte{3, 0x11}), 3*625*625*64 + scale},
		{"a baseline JPEG whose Adobe segment says RGB", MimeJPEG, jpegHeader(adobeRGBSegment, 0xc0, side, side,
			all, [2]byte{2, 0x11}, [2]byte{3, 0x11}), 3*625*625*64 + 4*side*side + scale},
		{"a baseline JPEG whose components are R, G, and B", MimeJPEG, jpegHeader(nil, 0xc0, side, side, rgb...),
			3*625*625*64 + 4*side*side + scale},
		{"a grey JPEG, whose sampling is ignored", MimeJPEG, jpegHeader(jfifSegment, 0xc0, side, side,
			[2]byte{1, 0x22}), 625*625*64 + scale},
		// Junk before a segment, which image/jpeg skips, and which this reader
		// skips too, so the frame's header is read all the same.
		{"a JPEG with a stray byte before its frame", MimeJPEG, jpegHeader(append(slices.Clone(jfifSegment), 0),
			0xc0, side, side, all), 625*625*64 + scale},
		// An escaped 0xff00 before the frame, which the decoder treats as
		// extraneous data, not a segment, so this reader reads the real frame
		// rather than a smaller one that its bytes would spell as a length.
		{"a JPEG with an escaped 0xff before its frame", MimeJPEG,
			jpegHeader(append(slices.Clone(jfifSegment), 0xff, 0x00), 0xc0, side, side, all), 625*625*64 + scale},
		// image/jpeg decides whether 3 components are RGB once it has read the
		// whole file, from the last APP0 segment, and the last Adobe segment,
		// which may follow the frame's header, or a scan.
		{"an Adobe segment after the frame's header", MimeJPEG, jpegFile(nil, adobeRGBSegment, nil, 0xc2, side,
			side, sub...), sub420 + 4*side*side + scale},
		{"an Adobe segment after a scan", MimeJPEG, jpegFile(nil, nil, adobeRGBSegment, 0xc2, side, side, sub...),
			sub420 + 4*side*side + scale},
		{"JFIF, and then another APP0 segment, of R, G, and B", MimeJPEG, jpegFile(append(slices.Clone(jfifSegment),
			jfxxSegment...), nil, nil, 0xc2, side, side, rgbSub...), sub420 + 4*side*side + scale},
		{"JFIF after a scan, of R, G, and B", MimeJPEG, jpegFile(nil, nil, jfifSegment, 0xc2, side, side, rgbSub...),
			sub420 + scale},
		{"a lossy WebP", MimeWebP, lossy, 384*313*313 + int64(len(lossy)) + scale},
		{"a lossless WebP", MimeWebP, lossless, 11*side*side + webpCodesBytes + int64(len(lossless)) + scale},
		{"a WebP that has alpha", MimeWebP, alpha, 11*side*side + webpCodesBytes + int64(len(alpha)) + scale},
		{"an RGBA PNG", MimePNG, pngWith(side, side, 8, 6, false), 4*side*side + scale},
		{"a paletted PNG", MimePNG, pngWith(side, side, 8, 3, false), side*side + scale},
		{"a grey PNG of 16 bits", MimePNG, pngWith(side, side, 16, 0, false), 8*side*side + scale},
		{"an interlaced RGBA PNG of 16 bits", MimePNG, pngWith(side, side, 16, 6, true), 16*side*side + scale},
		// An interlaced frame, whose rows the decoder copies into their order,
		// takes another byte a pixel.
		{"a GIF", MimeGIF, []byte("GIF89a\x88\x13\x88\x13\x00\x00\x00"), 2*side*side + scale},
	} {
		img, err := Sniff(tt.data, testLimit)
		if err != nil || img.Mime != tt.mime {
			t.Errorf("%s: %s, %v", tt.name, img.Mime, err)
			continue
		}
		if got := decodeBytes(img); got != tt.want {
			t.Errorf("%s: %d bytes, want %d", tt.name, got, tt.want)
		}
	}

	if _, err := Thumbnail(cmyk, MimeJPEG); err == nil || !strings.Contains(err.Error(), "MiB of memory") {
		t.Errorf("a progressive CMYK JPEG of %d pixels: %v, want no thumbnail for its memory", side*side, err)
	}
	// A photo of 24 million pixels, whose colours are subsampled 4:2:0: 375
	// × 250 units of 4 blocks of luma and 2 of chroma.
	photo, err := Sniff(jpegHeader(jfifSegment, 0xc2, 6000, 4000, [2]byte{1, 0x22}, [2]byte{2, 0x11},
		[2]byte{3, 0x11}), testLimit)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := decodeBytes(photo), int64(375*250*6*(64+256)+32*ThumbnailSide*4000); got != want ||
		got > maxDecodeBytes {
		t.Errorf("a progressive photo of 24 million pixels: %d bytes, want %d, at most %d", got, want,
			maxDecodeBytes)
	}
	// An upload of an image whose thumbnail would take too much memory makes
	// it its own thumbnail at once, rather than enqueue a job that would
	// read the whole file to learn so.
	costly, err := Sniff(cmyk, testLimit)
	if err != nil {
		t.Fatal(err)
	}
	if needsThumbnail(costly) || !needsThumbnail(photo) {
		t.Errorf("a thumbnail of its own: %t for the CMYK image, %t for the photo; want false and true",
			needsThumbnail(costly), needsThumbnail(photo))
	}
	// A progressive photo whose Adobe segment, after its scan, makes the
	// decoder convert it to RGBA takes more than a thumbnail may, and a lossy
	// WebP of as many pixels less.
	for _, tt := range []struct {
		name string
		data []byte
		want bool
	}{
		{"a progressive JPEG made RGB after its scan", jpegFile(nil, nil, adobeRGBSegment, 0xc2, side, side, sub...),
			false},
		{"a lossy WebP", lossy, true},
		{"a lossless WebP", lossless, false},
	} {
		img, err := Sniff(tt.data, testLimit)
		if err != nil {
			t.Fatal(err)
		}
		if got := needsThumbnail(img); got != tt.want {
			t.Errorf("%s of %d pixels: a thumbnail of its own %t, want %t", tt.name, side*side, got, tt.want)
		}
	}
}

// TestDecodeBytesBoundsDecoders checks decodeBytes against what making a
// thumbnail allocates in fact, of images in the encodings that the standard
// library writes, a baseline JPEG of YCbCr whose colours are subsampled
// 4:2:0, or grey, a PNG of RGBA of 8 or 16 bits a channel, or paletted, or
// grey, and a GIF, so that a decoder of a later Go, or of golang.org/x/image,
// that allocates more for each pixel than decodeBytes counts fails it, rather
// than let a thumbnail take more than maxDecodeBytes unnoticed. Besides what
// decodeBytes counts, a thumbnail takes about 1.5 MiB whatever the image's
// size, as the encoder of its PNG does, which is less than slack. Progressive
// JPEGs and WebP images, which no encoder of Go writes, are checked by
// TestDecodeBytes alone.
func TestDecodeBytesBoundsDecoders(t *testing.T) {
	const w, h, slack = 2400, 1600, 2 << 20
	nrgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	deep := image.NewNRGBA64(image.Rect(0, 0, w, h))
	paletted := image.NewPaletted(image.Rect(0, 0, w, h), palette.Plan9)
	grey := image.NewGray(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.NRGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255}
			nrgba.SetNRGBA(x, y, c)
			deep.Set(x, y, c)
			paletted.SetColorIndex(x, y, uint8((x+y)%200))
			grey.SetGray(x, y, color.Gray{Y: uint8(x * y)})
		}
	}
	for _, tt := range []struct {
		name   string
		mime   string
		encode func(*bytes.Buffer) error
	}{
		{"a baseline JPEG", MimeJPEG, func(b *bytes.Buffer) error { return jpeg.Encode(b, nrgba, nil) }},
		{"a grey JPEG", MimeJPEG, func(b *bytes.Buffer) error { return jpeg.Encode(b, grey, nil) }},
		{"an RGBA PNG", MimePNG, func(b *bytes.Buffer) error { return png.Encode(b, nrgba) }},
		{"an RGBA PNG of 16 bits", MimePNG, func(b *bytes.Buffer) error { return png.Encode(b, deep) }},
		{"a paletted PNG", MimePNG, func(b *bytes.Buffer) error { return png.Encode(b, paletted) }},
		{"a grey PNG", MimePNG, func(b *bytes.Buffer) error { return png.Encode(b, grey) }},
		{"a GIF", MimeGIF, func(b *bytes.Buffer) error { return gif.Encode(b, paletted, nil) }},
	} {
		var b bytes.Buffer
		if err := tt.encode(&b); err != nil {
			t.Fatal(err)
		}
		data := b.Bytes()
		img, err := Sniff(data, len(data))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err = Thumbnail(data, tt.mime)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got, want := int64(after.TotalAlloc-before.TotalAlloc), decodeBytes(img); got > want+slack {
			t.Errorf("%s of %d × %d: its thumbnail allocated %d bytes, and decodeBytes counts %d", tt.name, w, h,
				got, want)
		}
	}
}

// TestGIFThumbnail checks that the thumbnail of a GIF image whose first
// frame covers only part of the image's screen shows the whole screen, with
// the frame in its place, scaled as the screen is, and the rest transparent,
// rather than the frame stretched to fill it.
func TestGIFThumbnail(t *testing.T) {
	frame := image.NewPaletted(image.Rect(100, 500, 400, 600), palette.Plan9)
	red := uint8(color.Palette(palette.Plan9).Index(color.RGBA{R: 255, A: 255}))
	for i := range frame.Pix {
		frame.Pix[i] = red
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, &gif.GIF{Image: []*image.Paletted{frame}, Delay: []int{0},
		Config: image.Config{Width: 1000, Height: 1000, ColorModel: color.Palette(palette.Plan9)}}); err != nil {
		t.Fatal(err)
	}
	thumb, err := Thumbnail(b.Bytes(), MimeGIF)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatal(err)
	}
	if bounds := img.Bounds(); bounds.Dx() != ThumbnailSide || bounds.Dy() != ThumbnailSide {
		t.Fatalf("the thumbnail is %d × %d, want %d × %d", bounds.Dx(), bounds.Dy(), ThumbnailSide, ThumbnailSide)
	}
	// The frame is at 25.6 to 102.4 across, and 128 to 153.6 down.
	at := func(x, y int) color.NRGBA { return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA) }
	if c := at(64, 140); c.R < 200 || c.A != 255 {
		t.Errorf("the frame's place is %v, want red", c)
	}
	for _, p := range []image.Point{{10, 10}, {200, 140}, {64, 200}} {
		if c := at(p.X, p.Y); c.A != 0 {
			t.Errorf("%v, outside the frame, is %v, want transparent", p, c)
		}
	}
}

// TestOrient checks each orientation against the pixel that it shows at the
// top left, of an image of 3 × 2 pixels whose every pixel differs.
func TestOrient(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for y := range 2 {
		for x := range 3 {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), A: 255})
		}
	}
	for _, tt := range []struct {
		o, w, h, x, y int
	}{
		// The pixel of img shown at the top left, and the size it is shown at.
		{1, 3, 2, 0, 0}, {2, 3, 2, 2, 0}, {3, 3, 2, 2, 1}, {4, 3, 2, 0, 1},
		{5, 2, 3, 0, 0}, {6, 2, 3, 0, 1}, {7, 2, 3, 2, 1}, {8, 2, 3, 2, 0},
	} {
		got := orient(img, tt.o)
		b := got.Bounds()
		if c := got.NRGBAAt(0, 0); b.Dx() != tt.w || b.Dy() != tt.h || int(c.R) != tt.x || int(c.G) != tt.y {
			t.Errorf("the orientation %d: %d × %d, the top left is %d, %d; want %d × %d and %d, %d", tt.o,
				b.Dx(), b.Dy(), c.R, c.G, tt.w, tt.h, tt.x, tt.y)
		}
	}
}

// namespaced returns the start tag of a g element that declares n
// namespaces, and those of SVG's namespace, so that the namespaces in scope
// can be counted (maxSVGAttrs).
func namespaced(n int) string {
	var b strings.Builder
	b.WriteString(`<g`)
	for i := range n {
		fmt.Fprintf(&b, ` xmlns:n%d="urn:n"`, i)
	}
	b.WriteString(`>`)
	return b.String()
}

// svgWithAttrs returns an SVG whose rect element has n distinct attributes,
// so that an element's attributes can be counted (maxSVGAttrs).
func svgWithAttrs(n int) string {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg"><rect`)
	for i := range n {
		fmt.Fprintf(&b, ` a%d="1"`, i)
	}
	b.WriteString(`/></svg>`)
	return b.String()
}

// utf16Of returns s, which is ASCII, in UTF-16 of the byte order, after its
// byte order mark, as an editor on Windows may write an SVG.
func utf16Of(order binary.AppendByteOrder, s string) string {
	b := order.AppendUint16(nil, 0xfeff)
	for _, c := range []byte(s) {
		b = order.AppendUint16(b, uint16(c))
	}
	return string(b)
}

// TestSanitizeSVG checks what an SVG keeps and what it loses: its scripts,
// event handlers, animations, foreign content, and every reference outside
// it go, and what draws the image stays, with the namespaces of its
// elements and attributes resolved.
//
// Verifies: FR-ASSET-02/AC2
func TestSanitizeSVG(t *testing.T) {
	// An element of exactly the most attributes is kept, one more refused.
	if _, err := sanitizeSVG([]byte(svgWithAttrs(maxSVGAttrs)), testLimit); err != nil {
		t.Errorf("an element of %d attributes: %v", maxSVGAttrs, err)
	}
	// Elements that declare as many namespaces each one after another, rather
	// than one inside another, are kept: an element's namespaces are out of
	// scope once it ends.
	siblings := `<svg xmlns="http://www.w3.org/2000/svg">` + namespaced(maxSVGAttrs/2) + `</g>` +
		namespaced(maxSVGAttrs/2) + `</g>` + namespaced(maxSVGAttrs/2) + `</g></svg>`
	if _, err := sanitizeSVG([]byte(siblings), testLimit); err != nil {
		t.Errorf("elements one after another that declare %d namespaces each: %v", maxSVGAttrs/2, err)
	}
	for _, tt := range []struct {
		name     string
		in       string
		keep     []string
		drop     []string
		w, h     int
		noResult bool
	}{
		{
			name: "scripts and handlers",
			in: `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="20" onload="x()"><script>x()</script>` +
				`<SCRIPT>y()</SCRIPT><rect width="5" height="5" ONCLICK="x()"/></svg>`,
			keep: []string{`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="20">`,
				`<rect width="5" height="5"></rect>`},
			drop: []string{"script", "SCRIPT", "x()", "y()", "onload", "ONCLICK"},
			w:    10, h: 20,
		},
		{
			name: "references outside the file",
			in: `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 64.5 32">` +
				`<use xlink:href="#a"/><use href="https://x.example/s.svg#a"/><use xlink:href="//x.example/s"/>` +
				`<image href="data:image/png;base64,AAAA"/><image href="data:image/svg+xml;base64,AAAA"/>` +
				`<a href="javascript:x()"><text>t</text></a><feImage href="https://x.example/i.png"/></svg>`,
			keep: []string{`<use href="#a"></use>`, `<image href="data:image/png;base64,AAAA"></image>`,
				`<a><text>t</text></a>`, "<feImage></feImage>"},
			drop: []string{"x.example", "svg+xml", "javascript"},
			w:    65, h: 32,
		},
		{
			name: "URLs in other attributes",
			in: `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">` +
				`<a href="#x" ping="https://t.example/p"><text>t</text></a><rect ping="a https://t.example/q"/>` +
				`<rect ping="https&#9;://t.example/r"/><rect attributionsrc="//t.example/s"/>` +
				`<rect ping=" /&#10;/t.example/u"/><rect data-path="a/b" class="c d"/></svg>`,
			keep: []string{`<a href="#x"><text>t</text></a>`, `<rect data-path="a/b" class="c d"></rect>`},
			drop: []string{"t.example", "ping", "attributionsrc"},
		},
		{
			// A browser drops a space only where a URL starts, so a space before
			// a colon makes no scheme, and a path whose second slash a space
			// follows is no host's. requiredExtensions names an extension that
			// a browser compares, and never requests, and keeps an element that
			// names one that it does not have from being shown. A word that
			// ends with a colon is dropped all the same.
			name: "names and conditions",
			in: `<svg xmlns="http://www.w3.org/2000/svg"><switch>` +
				`<g requiredExtensions="http://ns.adobe.com/AdobeIllustrator/10.0/" data-name="Layer 1: copy"/>` +
				`<g requiredFeatures="http://www.w3.org/TR/SVG11/feature#Extensibility" data-path="/ /x"/>` +
				`</switch><rect aria-label="Note: see below"/></svg>`,
			keep: []string{`<g requiredExtensions="http://ns.adobe.com/AdobeIllustrator/10.0/" ` +
				`data-name="Layer 1: copy"></g>`,
				`<g requiredFeatures="http://www.w3.org/TR/SVG11/feature#Extensibility" data-path="/ /x"></g>`,
				`<rect></rect>`},
			drop: []string{"Note"},
		},
		{
			name: "repeated attributes",
			in: `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">` +
				`<use xlink:href="#a" href="#b"/><rect fill="red" fill="blue"/></svg>`,
			keep: []string{`<use href="#a"></use>`, `<rect fill="red"></rect>`},
			drop: []string{"#b", "blue"},
		},
		{
			name: "CSS",
			in: `<svg xmlns="http://www.w3.org/2000/svg"><style>.a{fill:url(#g)}</style>` +
				`<style>@import url(x.css);</style><style>.b{background:url(https://x.example/i)}</style>` +
				`<rect class="important" fill="url(#g)" style="fill: url( '#g' )"/>` +
				`<rect fill="url(https://x.example/p)" style="background-image:image-set('x' 1x)"/>` +
				`<rect style="fill:u\72l(https://x.example)"/><rect cursor="url(x.cur)"/>` +
				`<rect style="fill: image('https://y.example/t.png')"/><style>.c{fill:image("//z.example/t")}</style>` +
				`<rect style="fill: IMAGE('https://w.example/t.png')"/><rect fill="Image-Set('v.example' 1x)"/>` +
				`<style>.d{behavior:url(#a);x:JavaScript:alert(1)}</style></svg>`,
			keep: []string{"<style>.a{fill:url(#g)}</style>",
				`<rect class="important" fill="url(#g)" style="fill: url( '#g' )"></rect>`},
			drop: []string{"@import", "x.example", "image-set", `\72`, "x.cur", "y.example", "z.example", "w.example",
				"v.example", "alert"},
		},
		{
			name: "foreign content and animations",
			in: `<svg xmlns="http://www.w3.org/2000/svg" xmlns:i="http://ns.example/editor" i:layer="1">` +
				`<foreignObject><div xmlns="http://www.w3.org/1999/xhtml">hi</div></foreignObject>` +
				`<set attributeName="href" to="javascript:x()"/><animate attributeName="x"/>` +
				`<i:meta>editor</i:meta><g i:id="1"><!-- a comment --><?pi data?><circle r="1"/></g></svg>`,
			keep: []string{"<g><circle r=\"1\"></circle></g>"},
			drop: []string{"foreignObject", "hi", "set", "animate", "editor", "layer", "comment", "pi data"},
		},
		{
			name: "Illustrator's entities",
			in: `<?xml version="1.0"?><!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" ` +
				`"http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd" [<!ENTITY ns_svg "http://www.w3.org/2000/svg">]>` +
				`<svg xmlns="&ns_svg;" width="3px" height="4"><path d="M0 0"/></svg>`,
			keep: []string{`<path d="M0 0"></path>`},
			w:    3, h: 4,
		},
		{
			name: "Illustrator's styles",
			in: `<!DOCTYPE svg [<!ENTITY st0 "fill:#FFFFFF;stroke:#000000;">]>` +
				`<svg xmlns="http://www.w3.org/2000/svg"><rect style="&st0;"/><circle style="&st0;"/></svg>`,
			keep: []string{`<rect style="fill:#FFFFFF;stroke:#000000;"></rect>`,
				`<circle style="fill:#FFFFFF;stroke:#000000;"></circle>`},
			drop: []string{"st0"},
		},
		{
			name: "a prefixed root",
			in:   `<s:svg xmlns:s="http://www.w3.org/2000/svg" width="100%"><s:g/></s:svg>`,
			keep: []string{`<svg xmlns="http://www.w3.org/2000/svg" width="100%"><g></g></svg>`},
		},
		// Only the equals signs of a start tag count its attributes, so text,
		// CSS, a comment, a section of CDATA, or a processing instruction of
		// many is sanitized, rather than refused.
		{
			name: "many equals signs outside the tags",
			in: `<?xml-stylesheet ` + strings.Repeat(`a="b" `, 2*maxSVGAttrs) + `?>` +
				`<svg xmlns="http://www.w3.org/2000/svg"><!--` + strings.Repeat("=", 2*maxSVGAttrs) + `-->` +
				`<style>` + strings.Repeat(`rect[data-x="1"]{fill:red}`, 2*maxSVGAttrs) + `</style>` +
				`<script><![CDATA[` + strings.Repeat("a=1;", 2*maxSVGAttrs) + `]]></script>` +
				`<text>` + strings.Repeat("=", 2*maxSVGAttrs) + `</text></svg>`,
			keep: []string{`<style>rect[data-x="1"]{fill:red}`, "<text>" + strings.Repeat("=", 2*maxSVGAttrs)},
			drop: []string{"script", "a=1", "xml-stylesheet", "<!--"},
		},
		// An id or a class names the element, whatever it holds, as an editor
		// writes one from a layer's name, which a reference then finds.
		{
			name: "ids and classes that look like URLs or CSS",
			in: `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">` +
				`<defs><circle id="icon@2x" r="3"/><rect id="Layer: 1" class="md:fill-red a\b"/></defs>` +
				`<use href="#icon@2x"/><use xlink:href="#Layer: 1"/><rect data-name="Layer: 1"/></svg>`,
			keep: []string{`<circle id="icon@2x" r="3"></circle>`, `<rect id="Layer: 1" class="md:fill-red a\b">`,
				`<use href="#icon@2x"></use>`, `<use href="#Layer: 1"></use>`, `<rect></rect>`},
			drop: []string{"data-name"},
		},
		// Sizes in absolute units, at 96 pixels to the inch, as Inkscape's A4
		// page, of 210mm × 297mm.
		{
			name: "a size in millimetres",
			in:   `<svg xmlns="http://www.w3.org/2000/svg" width="210mm" height="297MM" viewBox="0 0 210 297"/>`,
			keep: []string{`width="210mm"`},
			w:    794, h: 1123,
		},
		{
			name: "a size in points and inches",
			in:   `<svg xmlns="http://www.w3.org/2000/svg" width="18pt" height="0.25in"/>`,
			w:    24, h: 24,
		},
		// 6.35mm is 24 pixels, which the conversion's error does not make 25.
		{
			name: "a size whose conversion is not exact",
			in:   `<svg xmlns="http://www.w3.org/2000/svg" width="6.35mm" height="12.7mm"/>`,
			w:    24, h: 48,
		},
		// A side in a unit that says no size by itself, as a percentage, or a
		// font's, is the other's in the proportions of the viewBox.
		{
			name: "one side in pixels",
			in:   `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="50%" viewBox="0 0 400 300"/>`,
			w:    100, h: 75,
		},
		{
			name: "one side in millimetres",
			in:   `<svg xmlns="http://www.w3.org/2000/svg" height="25.4mm" viewBox="0 0 30 60"/>`,
			w:    48, h: 96,
		},
		{
			name: "sides in a font's units",
			in:   `<svg xmlns="http://www.w3.org/2000/svg" width="2em" height="2em" viewBox="0 0 16 8"/>`,
			w:    16, h: 8,
		},
		// A size of more than MaxSide on a side is none, rather than a size
		// that the viewBox's proportions make of the other side.
		{
			name: "a side of more than MaxSide",
			in:   `<svg xmlns="http://www.w3.org/2000/svg" width="20000" height="100" viewBox="0 0 10 10"/>`,
		},
		{
			name: "a viewBox whose proportions make a side of more than MaxSide",
			in:   `<svg xmlns="http://www.w3.org/2000/svg" height="100" viewBox="0 0 1000 1"/>`,
		},
		// A viewBox's user units are no pixels once a side is given.
		{
			name: "a viewBox of more than MaxSide user units",
			in:   `<svg xmlns="http://www.w3.org/2000/svg" width="100" viewBox="0 0 100000 50000"/>`,
			w:    100, h: 50,
		},
	} {
		img, err := sanitizeSVG([]byte(tt.in), testLimit)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		out := string(img.Data)
		for _, k := range tt.keep {
			if !strings.Contains(out, k) {
				t.Errorf("%s: lost %q:\n%s", tt.name, k, out)
			}
		}
		for _, d := range tt.drop {
			if strings.Contains(out, d) {
				t.Errorf("%s: kept %q:\n%s", tt.name, d, out)
			}
		}
		if img.Width != tt.w || img.Height != tt.h || img.Mime != MimeSVG || img.Ext != "svg" {
			t.Errorf("%s: %s, %d × %d, want %d × %d", tt.name, img.Mime, img.Width, img.Height, tt.w, tt.h)
		}
		// What the sanitizer writes, it keeps as it is.
		again, err := sanitizeSVG(img.Data, testLimit)
		if err != nil || !bytes.Equal(again.Data, img.Data) {
			t.Errorf("%s: sanitizing the result again changes it: %v\n%s\n%s", tt.name, err, img.Data, again.Data)
		}
	}

	for _, tt := range []struct {
		name, in, reason string
	}{
		{"no namespace", `<svg width="1"/>`, ReasonType},
		{"another root", `<html xmlns="http://www.w3.org/1999/xhtml"/>`, ReasonType},
		{"not XML", `{"svg": true}`, ReasonType},
		{"text not in UTF-8", "a caption\xff", ReasonType},
		{"not UTF-8", "<svg xmlns=\"http://www.w3.org/2000/svg\">\xff</svg>", ReasonMalformed},
		{"not the UTF-8 that it declares", `<?xml version="1.0" encoding="UTF-8"?>` +
			"<svg xmlns=\"http://www.w3.org/2000/svg\"><title>Caf\xe9</title></svg>", ReasonMalformed},
		// A file in UTF-16 is well-formed XML, which the decoder has no reader
		// of, as it has none of ISO-8859-1, whether or not the file's bytes
		// happen to be UTF-8 too, as they are when it holds only ASCII.
		{"UTF-16", utf16Of(binary.LittleEndian, `<svg xmlns="http://www.w3.org/2000/svg"/>`), ReasonUnsupported},
		{"UTF-16, big-endian", utf16Of(binary.BigEndian, `<?xml version="1.0" encoding="UTF-16"?><svg:svg `+
			`xmlns:svg="http://www.w3.org/2000/svg"/>`), ReasonUnsupported},
		{"UTF-16 without a byte order mark", utf16Of(binary.LittleEndian,
			`<svg xmlns="http://www.w3.org/2000/svg"/>`)[2:], ReasonUnsupported},
		{"UTF-16, big-endian, without a byte order mark", utf16Of(binary.BigEndian,
			`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)[2:], ReasonUnsupported},
		{"UTF-16 of an odd length", utf16Of(binary.LittleEndian, `<svg xmlns="http://www.w3.org/2000/svg"/>`) +
			"\x00", ReasonUnsupported},
		{"text in UTF-16", utf16Of(binary.LittleEndian, "a caption <b>"), ReasonType},
		{"a PNG after a byte order mark", "\xff\xfe\x89PNG\r\n\x1a\n", ReasonType},
		{"a byte order mark alone", "\xff\xfe", ReasonType},
		{"another encoding, in its bytes too", `<?xml version="1.0" encoding="ISO-8859-1"?>` +
			"<svg xmlns=\"http://www.w3.org/2000/svg\"><title>Caf\xe9</title></svg>", ReasonUnsupported},
		{"a broken root", `<svg xmlns="http://www.w3.org/2000/svg" width="10" <rect/>`, ReasonMalformed},
		{"a broken prefixed root", `<?xml version="1.0"?><s:svg xmlns:s="http://www.w3.org/2000/svg" a=1/>`,
			ReasonMalformed},
		{"a broken element of another kind", `<html <body>`, ReasonType},
		{"unclosed", `<svg xmlns="http://www.w3.org/2000/svg"><g>`, ReasonMalformed},
		{"two roots", `<svg xmlns="http://www.w3.org/2000/svg"/><svg xmlns="http://www.w3.org/2000/svg"/>`,
			ReasonMalformed},
		{"an unknown entity", `<svg xmlns="http://www.w3.org/2000/svg">&x;</svg>`, ReasonMalformed},
		{"an external entity", `<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]>` +
			`<svg xmlns="http://www.w3.org/2000/svg">&x;</svg>`, ReasonUnsupported},
		{"nested entities", `<!DOCTYPE svg [<!ENTITY a "1"><!ENTITY b "&a;&a;">]>` +
			`<svg xmlns="http://www.w3.org/2000/svg">&b;</svg>`, ReasonUnsupported},
		// Each document type would have the references counted again.
		{"two document types", `<!DOCTYPE svg [<!ENTITY a "1">]><!DOCTYPE svg [<!ENTITY b "2">]>` +
			`<svg xmlns="http://www.w3.org/2000/svg">&a;&b;</svg>`, ReasonMalformed},
		{"declarations that are no document type", strings.Repeat("<!x[>", 1000) +
			`<svg xmlns="http://www.w3.org/2000/svg"/>`, ReasonMalformed},
		// 5,000 references of 3 bytes to an entity of 1,000 expand to 5 MB.
		{"entities that expand too far", `<!DOCTYPE svg [<!ENTITY a "` + strings.Repeat("A", 1000) + `">]>` +
			`<svg xmlns="http://www.w3.org/2000/svg"><text>` + strings.Repeat("&a;", 5000) + `</text></svg>`,
			ReasonUnsupported},
		{"too deep", `<svg xmlns="http://www.w3.org/2000/svg">` + strings.Repeat("<g>", maxSVGDepth) +
			strings.Repeat("</g>", maxSVGDepth) + `</svg>`, ReasonUnsupported},
		// An element of far more attributes than a drawing has, which the
		// decoder would otherwise hold at once.
		{"too many attributes", svgWithAttrs(maxSVGAttrs + 1), ReasonUnsupported},
		{"many equals signs in a file that is no SVG", "<p " + strings.Repeat("=", maxSVGAttrs+1), ReasonType},
		// Elements that enclose one another, each of which declares fewer
		// namespaces than an element may have attributes, but which the
		// decoder holds together, until they end.
		{"too many namespaces in scope", `<svg xmlns="http://www.w3.org/2000/svg">` +
			namespaced(maxSVGAttrs/2) + namespaced(maxSVGAttrs/2) + `</g></g></svg>`, ReasonUnsupported},
		{"another encoding", `<?xml version="1.0" encoding="ISO-8859-1"?><svg xmlns="http://www.w3.org/2000/svg"/>`,
			ReasonUnsupported},
		{"another version", `<?xml version="1.1"?><svg xmlns="http://www.w3.org/2000/svg"/>`, ReasonUnsupported},
	} {
		_, err := sanitizeSVG([]byte(tt.in), testLimit)
		var ie *ImageError
		if !errors.As(err, &ie) || ie.Reason != tt.reason {
			t.Errorf("%s: %v, want an ImageError of %s", tt.name, err, tt.reason)
		}
	}
}

// TestManyAttributes checks that an element of more attributes than one may
// have is refused before the decoder reads any of them, which it would hold
// at many times the bytes that they take in the file, as a megabyte of
// namespace declarations would take tens of megabytes of memory, by the
// equals signs of its start tag alone, so that a file whose text, CSS, or
// comments hold many is not refused.
func TestManyAttributes(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg"`)
	for i := 0; b.Len() < 1<<20; i++ {
		fmt.Fprintf(&b, ` xmlns:a%d=""`, i)
	}
	b.WriteString(`/>`)
	data := []byte(b.String())
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := sanitizeSVG(data, testLimit)
	runtime.ReadMemStats(&after)
	if ie, ok := errors.AsType[*ImageError](err); !ok || ie.Reason != ReasonUnsupported {
		t.Errorf("an element of %d bytes of attributes: %v, want an ImageError of %s", len(data), err,
			ReasonUnsupported)
	}
	if n := after.TotalAlloc - before.TotalAlloc; n > uint64(len(data)) {
		t.Errorf("refusing an element of %d bytes of attributes allocated %d bytes", len(data), n)
	}
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{svgWithAttrs(maxSVGAttrs), false},
		{svgWithAttrs(maxSVGAttrs + 1), true},
		// A start tag's equals signs count, and those of two tags apart.
		{`<a ` + strings.Repeat("=", maxSVGAttrs) + `><b ` + strings.Repeat("=", maxSVGAttrs) + `>`, false},
		{`<a ` + strings.Repeat("=", maxSVGAttrs+1) + `/>`, true},
		// A tag that does not end, which the decoder reads up to the next
		// less-than sign, or the end of the file.
		{`<a ` + strings.Repeat("=", maxSVGAttrs+1), true},
		{`<a ` + strings.Repeat("=", maxSVGAttrs+1) + `<b>`, true},
		// A greater-than sign in a quoted value does not end the tag, and an
		// equals sign there is no attribute's.
		{`<a b=">" ` + strings.Repeat("=", maxSVGAttrs) + `>`, true},
		{`<a b='` + strings.Repeat("=", 2*maxSVGAttrs) + `'>`, false},
		// The text between the tags, as CSS, and what has no attributes, a
		// comment, a section of CDATA, a processing instruction, a declaration,
		// and an end tag, do not count.
		{strings.Repeat("=", 2*maxSVGAttrs) + "<a>", false},
		{"<a>" + strings.Repeat("=", 2*maxSVGAttrs), false},
		{"<a><!--" + strings.Repeat("=", 2*maxSVGAttrs) + "-->", false},
		{"<a><![CDATA[" + strings.Repeat("x=1;", 2*maxSVGAttrs) + "]]></a>", false},
		{"<?pi " + strings.Repeat(`a="b" `, 2*maxSVGAttrs) + "?><a/>", false},
		{"<!DOCTYPE a " + strings.Repeat("=", 2*maxSVGAttrs) + "><a/>", false},
		{"<a></a " + strings.Repeat("=", 2*maxSVGAttrs) + ">", false},
		// A less-than sign that a name follows inside one of them, as a
		// script's in a section of CDATA, is no start tag, as it is none of
		// the decoder's.
		{"<a><script><![CDATA[for(i=0;i<n;i++){" + strings.Repeat("a=b;", 2*maxSVGAttrs) + "}]]></script></a>", false},
		{"<a><!-- <g " + strings.Repeat("x=1 ", 2*maxSVGAttrs) + "--></a>", false},
		{"<?pi <x " + strings.Repeat("a=1 ", 2*maxSVGAttrs) + "?><a/>", false},
		{"<!DOCTYPE a [<!-- <x " + strings.Repeat("=", 2*maxSVGAttrs) + "> -->]><a/>", false},
		// A start tag after one of them counts, wherever the markup that a
		// quoted string of the document type, or a section of CDATA, holds
		// would have it end, and a declaration nested in the document type,
		// or a comment there, does not end it.
		{`<!DOCTYPE a "<!--"><a ` + strings.Repeat("=", maxSVGAttrs+1) + `> -->`, true},
		{`<!DOCTYPE a '<?'><a ` + strings.Repeat("=", maxSVGAttrs+1) + `> ?>`, true},
		{`<![CDATA[<!--]]><a ` + strings.Repeat("=", maxSVGAttrs+1) + `> -->`, true},
		{`<!-- <![CDATA[ --><a ` + strings.Repeat("=", maxSVGAttrs+1) + `> ]]>`, true},
		{`<?pi <!-- ?><a ` + strings.Repeat("=", maxSVGAttrs+1) + `> -->`, true},
		{`<!DOCTYPE a [<!ENTITY e "v"> <!-- > --> ]><a ` + strings.Repeat("=", maxSVGAttrs+1) + `>`, true},
		{`<!DOCTYPE a [<x> <a ` + strings.Repeat("=", maxSVGAttrs+1) + `>]>`, false},
	} {
		if got := manyAttributes([]byte(tt.in)); got != tt.want {
			t.Errorf("manyAttributes of %.40q…: %t, want %t", tt.in, got, tt.want)
		}
	}
}

// TestAttributesPastAgreesWithDecoder checks attributesPast against the
// decoder that sanitizeSVG reads with, on documents made of pieces of markup
// picked at random: it reports a start tag of more attributes than the limit
// whenever the decoder returns one, so that no comment, section of CDATA,
// processing instruction, or declaration hides one from it, and, of a
// document that the decoder reads whole, only then, so that none of them
// makes it count attributes that no start tag has.
func TestAttributesPastAgreesWithDecoder(t *testing.T) {
	pieces := []string{"<a", "<b:c", ` x="1"`, " y='2'", ` z = "3"`, "=", ">", "/>", "</a>", "</b:c>", "<", "!",
		"<!--", "-->", "--", "-", "<![CDATA[", "]]>", "]", "[", "<?p ", "?>", "?", "<!DOCTYPE a ", "<!ENTITY e 'v'>",
		`"`, "'", "<!", "t", " ", "\n", "&amp;"}
	r := rand.New(rand.NewPCG(29, 37))
	runs := 300000
	if testing.Short() {
		runs = 30000
	}
	read := 0
	for range runs {
		var b strings.Builder
		for range 1 + r.IntN(16) {
			b.WriteString(pieces[r.IntN(len(pieces))])
		}
		doc := b.String()
		most, whole := decodedAttributes(doc)
		if whole {
			read++
		}
		for limit := range 3 {
			got := attributesPast([]byte(doc), limit)
			switch {
			case most > limit && !got:
				t.Fatalf("%q: the decoder returns a start tag of %d attributes, and attributesPast with %d finds "+
					"none of more", doc, most, limit)
			case whole && got != (most > limit):
				t.Fatalf("%q, which the decoder reads whole, with start tags of %d attributes at most: "+
					"attributesPast with %d reports %t", doc, most, limit, got)
			}
		}
	}
	// The documents that the decoder reads whole are what can show that
	// attributesPast counts too many.
	if read < runs/50 {
		t.Errorf("the decoder read %d of %d documents whole, too few to show anything", read, runs)
	}
}

// decodedAttributes returns the most attributes of a start tag that the
// decoder returns of doc, which reads it as sanitizeSVG does, without
// checking that its end tags match, so that it reads as many start tags as it
// can, and whether it reads doc whole.
func decodedAttributes(doc string) (int, bool) {
	d := xml.NewDecoder(strings.NewReader(doc))
	d.Strict = true
	d.CharsetReader = nil
	most := 0
	for {
		tok, err := d.RawToken()
		if err != nil {
			return most, errors.Is(err, io.EOF)
		}
		if s, ok := tok.(xml.StartElement); ok {
			most = max(most, len(s.Attr))
		}
	}
}

// TestSanitizedSize checks how many bytes a sanitized SVG takes: text that
// spans lines, as between the elements that an editor indented, takes no
// more than it did, a line feed or a tab in an attribute's value, which a
// parser would read as a space, is written as a reference, which keeps the
// value, and an image whose characters, as those of a section of CDATA, are
// written as references longer than the file was is refused once it would
// take more than the limit, rather than held whole.
func TestSanitizedSize(t *testing.T) {
	indented := `<svg xmlns="http://www.w3.org/2000/svg">` + strings.Repeat("\n\t<g d=\"a\n\tb\"/>", 1000) + "\n</svg>"
	img, err := sanitizeSVG([]byte(indented), testLimit)
	if err != nil {
		t.Fatal(err)
	}
	out := string(img.Data)
	if !strings.Contains(out, "\n\t<g d=") || strings.Contains(out, "&#xA;\t<g") ||
		!strings.Contains(out, `<g d="a&#xA;&#x9;b"></g>`) {
		t.Errorf("an indented image, sanitized:\n%.200s", out)
	}
	if again, err := sanitizeSVG(img.Data, testLimit); err != nil || !bytes.Equal(again.Data, img.Data) {
		t.Errorf("sanitizing the indented image again changes it: %v", err)
	}

	ampersands := `<svg xmlns="http://www.w3.org/2000/svg"><text><![CDATA[` + strings.Repeat("&", 10_000) +
		`]]></text></svg>`
	img, err = sanitizeSVG([]byte(ampersands), testLimit)
	if err != nil || strings.Count(string(img.Data), "&amp;") != 10_000 {
		t.Errorf("a section of CDATA of ampersands: %v", err)
	}
	if _, err := sanitizeSVG([]byte(ampersands), len(ampersands)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a section of CDATA of ampersands, which take five times as many bytes once sanitized, within "+
			"the size of the file: %v, want ErrTooLarge", err)
	}
	if _, err := Sniff([]byte(ampersands), 1000); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Sniff of an image that takes more than its limit once sanitized: %v, want ErrTooLarge", err)
	}
}

// TestDisposition checks the Content-Disposition that an asset's file is
// served with: inline for an image, which every type that an upload takes
// is, and an attachment, which a browser saves rather than shows, for any
// other type, with a file name made of the asset's ID and the extension of
// its key, whether it is the file or its thumbnail.
//
// Verifies: FR-ASSET-02/AC3
func TestDisposition(t *testing.T) {
	for _, tt := range []struct {
		contentType, key, want string
	}{
		{MimePNG, "w/abc.png", `inline; filename="asset_1.png"`},
		{MimeSVG, "w/abc.svg", `inline; filename="asset_1.svg"`},
		{MimePNG, "w/abc.thumb.png", `inline; filename="asset_1.png"`},
		{"application/pdf", "w/abc.pdf", `attachment; filename="asset_1.pdf"`},
		{"text/html", "w/abc.html", `attachment; filename="asset_1.html"`},
	} {
		loc := Located{Asset: Asset{ID: "asset_1"}, Key: tt.key, ContentType: tt.contentType}
		if got := Disposition(loc); got != tt.want {
			t.Errorf("the disposition of %s, %s: %s, want %s", tt.key, tt.contentType, got, tt.want)
		}
	}
}
