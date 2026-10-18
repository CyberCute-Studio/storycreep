package asset

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
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
		got, err := Sniff(tt.data)
		if err != nil || got.Mime != tt.mime || got.Ext != tt.ext || got.Width != 30 || got.Height != 20 ||
			!bytes.Equal(got.Data, tt.data) {
			t.Errorf("%s: %+v, %v", tt.name, got, err)
		}
	}
	if got, err := Sniff(minimalWebP); err != nil || got.Mime != MimeWebP || got.Width != 1 || got.Height != 1 {
		t.Errorf("WebP: %+v, %v", got, err)
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
		_, err := Sniff(tt.data)
		var ie *ImageError
		if !errors.As(err, &ie) || ie.Reason != tt.reason {
			t.Errorf("%s: %v, want an ImageError of %s", tt.name, err, tt.reason)
		}
	}
	if _, err := Sniff(pngOf(5000, 5000)); err != nil {
		t.Errorf("a PNG of %d pixels, the most: %v", MaxPixels, err)
	}
}

// pngOf returns a PNG file of a header that says width × height, and
// nothing else.
func pngOf(width, height int) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := []byte{0, 0, 0, 13, 'I', 'H', 'D', 'R',
		byte(width >> 24), byte(width >> 16), byte(width >> 8), byte(width),
		byte(height >> 24), byte(height >> 16), byte(height >> 8), byte(height), 8, 6, 0, 0, 0}
	b.Write(ihdr)
	sum := crc(ihdr[4:])
	b.Write([]byte{byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum)})
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
	ctx := context.Background()
	for _, tt := range []struct {
		width, height, wantW, wantH int
	}{
		{1024, 512, 256, 128}, {300, 900, 85, 256}, {3000, 2, 256, 1},
	} {
		data := encoded(t, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) },
			rgba(tt.width, tt.height))
		thumb, err := Thumbnail(ctx, data, MimePNG)
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
	if _, err := Thumbnail(ctx, jpg, MimeJPEG); err != nil {
		t.Errorf("a JPEG: %v", err)
	}
	if _, err := Thumbnail(ctx, jpg, MimePNG); err == nil {
		t.Error("a JPEG said to be a PNG has a thumbnail")
	}
	truncated := jpg[:len(jpg)/2]
	if _, err := Thumbnail(ctx, truncated, MimeJPEG); err == nil {
		t.Error("a truncated JPEG has a thumbnail")
	}
	if _, err := Thumbnail(ctx, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), MimeSVG); err == nil ||
		!strings.Contains(err.Error(), "no thumbnail") {
		t.Errorf("an SVG: %v", err)
	}
}

// TestSanitizeSVG checks what an SVG keeps and what it loses: its scripts,
// event handlers, animations, foreign content, and every reference outside
// it go, and what draws the image stays, with the namespaces of its
// elements and attributes resolved.
//
// Verifies: FR-ASSET-02/AC2
func TestSanitizeSVG(t *testing.T) {
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
			name: "CSS",
			in: `<svg xmlns="http://www.w3.org/2000/svg"><style>.a{fill:url(#g)}</style>` +
				`<style>@import url(x.css);</style><style>.b{background:url(https://x.example/i)}</style>` +
				`<rect class="important" fill="url(#g)" style="fill: url( '#g' )"/>` +
				`<rect fill="url(https://x.example/p)" style="background-image:image-set('x' 1x)"/>` +
				`<rect style="fill:u\72l(https://x.example)"/><rect cursor="url(x.cur)"/></svg>`,
			keep: []string{"<style>.a{fill:url(#g)}</style>",
				`<rect class="important" fill="url(#g)" style="fill: url( &#39;#g&#39; )"></rect>`},
			drop: []string{"@import", "x.example", "image-set", `\72`, "x.cur"},
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
			name: "a prefixed root",
			in:   `<s:svg xmlns:s="http://www.w3.org/2000/svg" width="100%"><s:g/></s:svg>`,
			keep: []string{`<svg xmlns="http://www.w3.org/2000/svg" width="100%"><g></g></svg>`},
		},
	} {
		img, err := sanitizeSVG([]byte(tt.in))
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
		again, err := sanitizeSVG(img.Data)
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
		{"not UTF-8", "<svg xmlns=\"http://www.w3.org/2000/svg\">\xff</svg>", ReasonType},
		{"unclosed", `<svg xmlns="http://www.w3.org/2000/svg"><g>`, ReasonMalformed},
		{"two roots", `<svg xmlns="http://www.w3.org/2000/svg"/><svg xmlns="http://www.w3.org/2000/svg"/>`,
			ReasonMalformed},
		{"an unknown entity", `<svg xmlns="http://www.w3.org/2000/svg">&x;</svg>`, ReasonMalformed},
		{"an external entity", `<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]>` +
			`<svg xmlns="http://www.w3.org/2000/svg">&x;</svg>`, ReasonMalformed},
		{"nested entities", `<!DOCTYPE svg [<!ENTITY a "1"><!ENTITY b "&a;&a;">]>` +
			`<svg xmlns="http://www.w3.org/2000/svg">&b;</svg>`, ReasonMalformed},
		{"too deep", `<svg xmlns="http://www.w3.org/2000/svg">` + strings.Repeat("<g>", maxSVGDepth) +
			strings.Repeat("</g>", maxSVGDepth) + `</svg>`, ReasonMalformed},
		{"another encoding", `<?xml version="1.0" encoding="ISO-8859-1"?><svg xmlns="http://www.w3.org/2000/svg"/>`,
			ReasonType},
	} {
		_, err := sanitizeSVG([]byte(tt.in))
		var ie *ImageError
		if !errors.As(err, &ie) || ie.Reason != tt.reason {
			t.Errorf("%s: %v, want an ImageError of %s", tt.name, err, tt.reason)
		}
	}
}
