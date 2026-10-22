package asset

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/webp"
)

// The types of file that an upload takes (PRD FR-ASSET-02), as the server
// finds them from the file's content, never from its name or the request's
// Content-Type.
const (
	MimePNG  = "image/png"
	MimeJPEG = "image/jpeg"
	MimeGIF  = "image/gif"
	MimeWebP = "image/webp"
	MimeSVG  = "image/svg+xml"
)

// extensions are the extensions of the types' keys, as the published
// format's asset paths have them (PRD §6.3, §7.5): png, jpg, webp, gif, and svg.
var extensions = map[string]string{
	MimePNG:  "png",
	MimeJPEG: "jpg",
	MimeGIF:  "gif",
	MimeWebP: "webp",
	MimeSVG:  "svg",
}

// The bounds of an image that an upload takes. An image is decoded whole to
// make its thumbnail (Thumbnail), which takes memory for each of its pixels,
// so a small file that claims a huge image cannot make the server decode one:
// an image of more than MaxPixels pixels, or wider or taller than MaxSide, is
// refused. How much memory each pixel takes depends on how the image is
// encoded too, so Thumbnail checks that as well (maxDecodeBytes).
const (
	MaxPixels = 25_000_000
	MaxSide   = 16384
)

// Reasons of an *ImageError.
const (
	// ReasonType is a file that is none of the types that an upload takes,
	// as text, a PDF, or a file whose extension says that it is an image
	// when its content is not (FR-ASSET-02 AC1).
	ReasonType = "type"
	// ReasonMalformed is a file that starts as one of the types, but whose
	// header does not decode, or an SVG that is not well-formed XML, as one
	// that names no encoding other than UTF-8, but is not in UTF-8.
	ReasonMalformed = "malformed"
	// ReasonUnsupported is an SVG that is well-formed XML, but that the
	// server does not take: one in UTF-16, or that declares another encoding
	// than UTF-8, or another version of XML than 1.0, whose elements nest too
	// deeply, or have, or declare, too many attributes or namespaces, or
	// whose document type declares entities that the server does not read,
	// or that expand too far (sanitizeSVG).
	ReasonUnsupported = "unsupported"
	// ReasonDimensions is an image larger than MaxPixels or MaxSide.
	ReasonDimensions = "dimensions"
)

// ImageError is a file that an upload refuses, and why (Reason).
type ImageError struct {
	Reason string
	// Detail says what is wrong, for people, in English, as the API's
	// problem does.
	Detail string
}

func (e *ImageError) Error() string {
	return "asset: the file is not an image that an upload takes: " + e.Detail
}

// Image is a file that an upload takes, as it is stored: an SVG once
// sanitize has removed what it does not allow.
type Image struct {
	// Mime is the type that the content says, and Ext the extension of its
	// key.
	Mime, Ext string
	// Width and Height are its size in pixels, as it is shown, or 0 for an
	// SVG that does not say it, or says one of more than MaxSide on a side.
	Width, Height int
	// Data is the file.
	Data []byte
}

// signatures are the first bytes of each raster type that an upload takes.
var signatures = []struct {
	mime   string
	prefix []byte
	config func([]byte) (image.Config, error)
}{
	{MimePNG, []byte("\x89PNG\r\n\x1a\n"), func(b []byte) (image.Config, error) { return png.DecodeConfig(bytes.NewReader(b)) }},
	{MimeJPEG, []byte("\xff\xd8\xff"), func(b []byte) (image.Config, error) { return jpeg.DecodeConfig(bytes.NewReader(b)) }},
	{MimeGIF, []byte("GIF87a"), func(b []byte) (image.Config, error) { return gif.DecodeConfig(bytes.NewReader(b)) }},
	{MimeGIF, []byte("GIF89a"), func(b []byte) (image.Config, error) { return gif.DecodeConfig(bytes.NewReader(b)) }},
}

// isWebP reports whether data starts as a WebP file does: a RIFF container
// of the form WEBP.
func isWebP(data []byte) bool {
	return len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP"))
}

// ErrTooLarge is returned for an SVG image that, sanitized, would take more
// bytes than the file that an upload may store.
var ErrTooLarge = errors.New("asset: the SVG image, sanitized, would be larger than an upload may store")

// Sniff returns the image that data holds, or an *ImageError if it is not
// one that an upload takes: a PNG, JPEG, GIF, or WebP file whose header
// decodes, of at most MaxPixels pixels and MaxSide on each side, or an SVG
// file, which it sanitizes (sanitizeSVG), and which must then take at most
// maxBytes, or it returns ErrTooLarge. The type comes from the content alone.
func Sniff(data []byte, maxBytes int) (Image, error) {
	for _, s := range signatures {
		if bytes.HasPrefix(data, s.prefix) {
			return raster(s.mime, data, s.config)
		}
	}
	if isWebP(data) {
		return raster(MimeWebP, data, func(b []byte) (image.Config, error) { return webp.DecodeConfig(bytes.NewReader(b)) })
	}
	svg, err := sanitizeSVG(data, maxBytes)
	if err != nil {
		return Image{}, err
	}
	return svg, nil
}

// raster returns the raster image of the type mime that data holds, whose
// header config decodes, or an *ImageError. The size of a JPEG image whose
// EXIF orientation turns it a quarter is the size that it is shown at, as a
// browser shows it, with its width and its height swapped.
func raster(mime string, data []byte, config func([]byte) (image.Config, error)) (Image, error) {
	cfg, err := config(data)
	if err != nil {
		return Image{}, &ImageError{Reason: ReasonMalformed,
			Detail: fmt.Sprintf("it starts as %s does, but its header does not decode: %v", mime, err)}
	}
	if err := checkSize(cfg.Width, cfg.Height); err != nil {
		return Image{}, err
	}
	width, height := cfg.Width, cfg.Height
	if mime == MimeJPEG && jpegOrientation(data) >= 5 {
		width, height = height, width
	}
	return Image{Mime: mime, Ext: extensions[mime], Width: width, Height: height, Data: data}, nil
}

// exifTag is the start of the APP1 segment of a JPEG file that holds its EXIF
// data, which a TIFF header and its directories follow.
var exifTag = []byte("Exif\x00\x00")

// jpegOrientation returns the orientation that the EXIF data of the JPEG
// file data says that its pixels are shown in, 1 to 8 (orient), or 1 if it
// says none, or data is not a JPEG file whose segments read up to it. Only
// the segments before the first scan are read, as EXIF is among them.
func jpegOrientation(data []byte) int {
	o := 1
	jpegSegments(data, func(marker byte, segment []byte) bool {
		switch {
		case marker == 0xda:
			return false
		case marker == 0xe1 && bytes.HasPrefix(segment, exifTag):
			o = tiffOrientation(segment[len(exifTag):])
			return false
		}
		return true
	})
	return o
}

// jpegSegments calls fn with the marker and the data of each segment of the
// JPEG file data, in order, a scan's header among them, until fn returns
// false, or the file ends, or a segment does not read, or data is not a JPEG
// file. It reads the markers as image/jpeg's own decoder does, so that it
// finds every segment that the decoder acts on, those between and after the
// scans too, and the same frame header: it skips the extraneous bytes that
// may fall between segments, the entropy-coded data of a scan, whose bytes
// the decoder reads as data until a marker, the escaped 0xff00 of that data,
// and the restart markers, rather than read any of them as a segment, which
// a crafted file could otherwise use to show this reader another frame, or
// other segments, than the decoder reads, and so a smaller estimate of the
// memory that decoding it takes (jpegDecodeBytes). The decoder refuses a
// marker that it does not know, as it does a frame's header of a type that
// it does not decode, without decoding the image; this reader passes them on.
func jpegSegments(data []byte, fn func(marker byte, segment []byte) bool) {
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return
	}
	for i := 2; i+2 <= len(data); {
		if data[i] != 0xff {
			// An extraneous byte between segments, or a byte of a scan's
			// entropy-coded data, which the decoder reads past until it
			// reaches the next marker.
			next := bytes.IndexByte(data[i:], 0xff)
			if next < 0 {
				return
			}
			i += next
			continue
		}
		marker := data[i+1]
		i += 2
		switch {
		case marker == 0x00:
			// An escaped 0xff of entropy-coded data, which is not a marker.
			continue
		case marker == 0xff:
			// A fill byte, which the marker follows.
			i--
			continue
		case marker == 0xd9:
			// The end of the image, at which the decoder stops.
			return
		case marker >= 0xd0 && marker <= 0xd7:
			// A restart marker, which has no segment.
			continue
		}
		// The length that follows the marker includes its own two bytes.
		if i+2 > len(data) {
			return
		}
		n := int(binary.BigEndian.Uint16(data[i:]))
		if n < 2 || i+n > len(data) || !fn(marker, data[i+2:i+n]) {
			return
		}
		i += n
	}
}

// tiffOrientation returns the orientation, tag 0x0112, of the first image
// directory of tiff, a TIFF header and its directories as EXIF holds them, or
// 1 if it has none that is valid.
func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:]) != 42 {
		return 1
	}
	dir := uint64(order.Uint32(tiff[4:]))
	if dir < 8 || dir+2 > uint64(len(tiff)) {
		return 1
	}
	entries := int(order.Uint16(tiff[dir:]))
	for k := range entries {
		e := int(dir) + 2 + 12*k
		if e+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[e:]) != 0x0112 {
			continue
		}
		// A SHORT, of which there is one, held in the entry itself.
		if order.Uint16(tiff[e+2:]) != 3 || order.Uint32(tiff[e+4:]) != 1 {
			return 1
		}
		if o := int(order.Uint16(tiff[e+8:])); o >= 1 && o <= 8 {
			return o
		}
		return 1
	}
	return 1
}

// checkSize returns an *ImageError for an image of width × height pixels
// that is empty, or larger than MaxPixels or MaxSide.
func checkSize(width, height int) error {
	switch {
	case width < 1 || height < 1:
		return &ImageError{Reason: ReasonMalformed, Detail: "it says that it has no pixels"}
	case width > MaxSide || height > MaxSide || int64(width)*int64(height) > MaxPixels:
		return &ImageError{Reason: ReasonDimensions, Detail: fmt.Sprintf("it is %d × %d pixels, and an image may "+
			"have at most %d pixels, and %d on each side", width, height, MaxPixels, MaxSide)}
	}
	return nil
}

// errNotImage is the ImageError of a file that is none of the types.
var errNotImage = &ImageError{Reason: ReasonType,
	Detail: "it is not a PNG, JPEG, GIF, WebP, or SVG image, as its content shows"}
