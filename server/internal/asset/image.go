package asset

import (
	"bytes"
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
// make its thumbnail (Thumbnail), which takes up to 8 bytes of memory for
// each of its pixels, so a small file that claims a huge image cannot make
// the server decode one: an image of more than MaxPixels pixels, or wider or
// taller than MaxSide, is refused.
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
	// header does not decode, or an SVG that is not well-formed XML.
	ReasonMalformed = "malformed"
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
	// Width and Height are its size in pixels, or 0 for an SVG that does not
	// say it.
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

// Sniff returns the image that data holds, or an *ImageError if it is not
// one that an upload takes: a PNG, JPEG, GIF, or WebP file whose header
// decodes, of at most MaxPixels pixels and MaxSide on each side, or an SVG
// file, which it sanitizes (sanitizeSVG). The type comes from the content
// alone.
func Sniff(data []byte) (Image, error) {
	for _, s := range signatures {
		if bytes.HasPrefix(data, s.prefix) {
			return raster(s.mime, data, s.config)
		}
	}
	if isWebP(data) {
		return raster(MimeWebP, data, func(b []byte) (image.Config, error) { return webp.DecodeConfig(bytes.NewReader(b)) })
	}
	svg, err := sanitizeSVG(data)
	if err != nil {
		return Image{}, err
	}
	return svg, nil
}

// raster returns the raster image of the type mime that data holds, whose
// header config decodes, or an *ImageError.
func raster(mime string, data []byte, config func([]byte) (image.Config, error)) (Image, error) {
	cfg, err := config(data)
	if err != nil {
		return Image{}, &ImageError{Reason: ReasonMalformed,
			Detail: fmt.Sprintf("it starts as %s does, but its header does not decode: %v", mime, err)}
	}
	if err := checkSize(cfg.Width, cfg.Height); err != nil {
		return Image{}, err
	}
	return Image{Mime: mime, Ext: extensions[mime], Width: cfg.Width, Height: cfg.Height, Data: data}, nil
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

// IsImageError reports whether err is an *ImageError.
func IsImageError(err error) bool {
	var e *ImageError
	return errors.As(err, &e)
}
