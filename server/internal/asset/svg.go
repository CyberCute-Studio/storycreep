package asset

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SVG files are sanitized, never rejected for what they hold (PRD FR-ASSET-02
// AC2, NFR-SEC-10): sanitizeSVG rewrites the file from what it reads, keeping
// only what an allowlist names, so that a file that the server stores holds
// no script and refers to nothing outside itself, and it never stores the
// file as it was uploaded. It keeps:
//
//   - the elements of SVG's namespace that draw, style, or structure an
//     image (svgElements), with their text, and drops any other element with
//     everything inside it: a script, a foreignObject, an animation, which
//     could set an attribute to a script's URL, or an element of another
//     namespace, as an editor's own;
//   - the attributes of no namespace whose names are names, and xml:space and
//     xml:lang, but not an event handler, whose name starts with "on";
//   - a reference, href or xlink:href, only to a fragment of the file, as
//     "#shape", or, on an image, a data: URL of a raster type that an upload
//     takes;
//   - a value that CSS reads, a style attribute, a style element, or any
//     other attribute, only if every url() in it refers to a fragment of the
//     file, and it has no backslash, which an escape could hide a URL
//     behind, no at-rule, as @import, and none of the other constructs of
//     CSS that load or run something (unsafeCSS).
//
// It drops comments, processing instructions, and the document type, and
// reads only the entities of the document type's internal subset whose values
// are text, as Illustrator declares the namespaces with, so that a file that
// refers to an external entity, or nests entities, is not well-formed to it.
// The file must be UTF-8, and its root an svg element of SVG's namespace.

const (
	svgNamespace   = "http://www.w3.org/2000/svg"
	xlinkNamespace = "http://www.w3.org/1999/xlink"
	xmlNamespace   = "http://www.w3.org/XML/1998/namespace"
)

// maxSVGDepth bounds how deeply an SVG's elements nest.
const maxSVGDepth = 256

// The bounds of the entities that an SVG's document type may declare.
const (
	maxSVGEntities      = 64
	maxSVGEntityLength  = 1024
	maxSVGEntityNameLen = 64
)

// svgElements are the elements that an SVG keeps: those of SVG 1.1 and SVG 2
// that draw, style, or structure a static image.
var svgElements = map[string]bool{}

func init() {
	for _, name := range strings.Fields(`a circle clipPath defs desc ellipse feBlend feColorMatrix
		feComponentTransfer feComposite feConvolveMatrix feDiffuseLighting feDisplacementMap feDistantLight
		feDropShadow feFlood feFuncA feFuncB feFuncG feFuncR feGaussianBlur feImage feMerge feMergeNode
		feMorphology feOffset fePointLight feSpecularLighting feSpotLight feTile feTurbulence filter g image
		line linearGradient marker mask path pattern polygon polyline radialGradient rect stop style svg switch
		symbol text textPath title tspan use view`) {
		svgElements[name] = true
	}
}

// attributeName matches the names of the attributes that an SVG keeps.
var attributeName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)

// dataImage matches a data: URL of a raster type that an upload takes, the
// only reference outside the file's fragments that an image element keeps.
var dataImage = regexp.MustCompile(`^data:image/(png|jpeg|gif|webp)[;,]`)

// entityDecl matches a declaration of an entity whose value is text, in a
// document type's internal subset.
var entityDecl = regexp.MustCompile(`<!ENTITY\s+([A-Za-z_][A-Za-z0-9_.-]*)\s+(?:"([^"%&<]*)"|'([^'%&<]*)')\s*>`)

// sanitizeSVG returns the SVG image that data holds, sanitized, or an
// *ImageError: errNotImage if data is not XML whose root is an svg element of
// SVG's namespace, and ReasonMalformed if it is, but is not well-formed, or
// nests too deeply.
func sanitizeSVG(data []byte) (Image, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		return Image{}, errNotImage
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	// A charset other than UTF-8 is refused: the decoder has no reader of it.
	d.CharsetReader = nil
	d.Entity = map[string]string{}

	var out bytes.Buffer
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	var img Image
	depth, skip := 0, 0
	rootSeen, rootDone := false, false
	// inStyle is set inside a style element that is kept, whose text is CSS.
	inStyle := false
	var styleText strings.Builder
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if !rootSeen {
				return Image{}, errNotImage
			}
			return Image{}, &ImageError{Reason: ReasonMalformed, Detail: "it is an SVG that is not well-formed XML"}
		}
		switch t := tok.(type) {
		case xml.Directive:
			if rootSeen {
				return Image{}, &ImageError{Reason: ReasonMalformed, Detail: "it has a document type inside its root"}
			}
			if err := declareEntities(d, t); err != nil {
				return Image{}, err
			}
		case xml.StartElement:
			if rootDone {
				return Image{}, &ImageError{Reason: ReasonMalformed, Detail: "it has more than one root element"}
			}
			depth++
			if depth > maxSVGDepth {
				return Image{}, &ImageError{Reason: ReasonMalformed,
					Detail: fmt.Sprintf("its elements nest more than %d deep", maxSVGDepth)}
			}
			if !rootSeen {
				rootSeen = true
				if t.Name.Space != svgNamespace || t.Name.Local != "svg" {
					return Image{}, errNotImage
				}
				img.Width, img.Height = svgSize(t.Attr)
			}
			if skip > 0 || inStyle || t.Name.Space != svgNamespace || !svgElements[t.Name.Local] {
				// An element that is dropped drops everything inside it, as does
				// one inside a style element, which holds only text.
				skip++
				continue
			}
			writeStart(&out, t, depth == 1)
			if t.Name.Local == "style" {
				inStyle = true
				styleText.Reset()
			}
		case xml.EndElement:
			depth--
			if depth == 0 {
				rootDone = true
			}
			if skip > 0 {
				skip--
				continue
			}
			if inStyle {
				inStyle = false
				if css := styleText.String(); !unsafeCSS(css) {
					xmlEscape(&out, css)
				}
			}
			out.WriteString("</" + t.Name.Local + ">")
		case xml.CharData:
			switch {
			case skip > 0 || depth == 0:
			case inStyle:
				styleText.Write(t)
			default:
				xmlEscape(&out, string(t))
			}
		}
	}
	if !rootSeen {
		return Image{}, errNotImage
	}
	img.Mime, img.Ext, img.Data = MimeSVG, extensions[MimeSVG], out.Bytes()
	return img, nil
}

// declareEntities adds to d's entities those that the document type dir
// declares in its internal subset with values that are text, or returns an
// *ImageError for a document type that declares more than that: an external
// entity, a parameter entity, an entity whose value refers to another, or
// too many or too long. Everything else of the document type is ignored.
func declareEntities(d *xml.Decoder, dir xml.Directive) error {
	text := string(dir)
	start := strings.IndexByte(text, '[')
	if start < 0 {
		return nil
	}
	subset := text[start:]
	malformed := &ImageError{Reason: ReasonMalformed, Detail: "its document type declares entities that the " +
		"server does not read: only an entity whose value is text, without a reference"}
	matches := entityDecl.FindAllStringSubmatch(subset, -1)
	if len(matches) != strings.Count(subset, "<!ENTITY") || len(matches) > maxSVGEntities {
		return malformed
	}
	for _, m := range matches {
		value := m[2] + m[3]
		if len(m[1]) > maxSVGEntityNameLen || len(value) > maxSVGEntityLength {
			return malformed
		}
		d.Entity[m[1]] = value
	}
	return nil
}

// writeStart writes the start tag of the element t, which is kept, with the
// attributes that it keeps, and, on the root, the namespace of SVG.
func writeStart(out *bytes.Buffer, t xml.StartElement, root bool) {
	out.WriteString("<" + t.Name.Local)
	if root {
		out.WriteString(` xmlns="` + svgNamespace + `"`)
	}
	seen := map[string]bool{}
	for _, a := range t.Attr {
		name, ok := attrName(a.Name)
		if !ok || seen[name] || !keepAttr(t.Name.Local, name, a.Value) {
			continue
		}
		seen[name] = true
		out.WriteString(" " + name + `="`)
		xmlEscape(out, a.Value)
		out.WriteString(`"`)
	}
	out.WriteString(">")
}

// attrName returns the name that the attribute a is written with, and true,
// or false for one that is dropped: a declaration of a namespace, which
// writeStart writes itself, an attribute of a namespace other than XML's and
// XLink's href, and an event handler.
func attrName(n xml.Name) (string, bool) {
	switch {
	case n.Space == "xmlns" || n.Space == "" && n.Local == "xmlns":
		return "", false
	case n.Space == xmlNamespace && (n.Local == "space" || n.Local == "lang"):
		return "xml:" + n.Local, true
	case n.Space == xlinkNamespace && n.Local == "href":
		// SVG 2 takes href without XLink, as every browser does.
		return "href", true
	case n.Space != "":
		return "", false
	case !attributeName.MatchString(n.Local) || strings.HasPrefix(strings.ToLower(n.Local), "on"):
		return "", false
	}
	return n.Local, true
}

// keepAttr reports whether an attribute name, as attrName writes it, of the
// element, keeps the value v.
func keepAttr(element, name, v string) bool {
	if name == "href" {
		return strings.HasPrefix(v, "#") || element == "image" && dataImage.MatchString(v)
	}
	return !unsafeCSS(v)
}

// cssURL matches the start of a url() of CSS, and what follows its
// parenthesis.
var cssURL = regexp.MustCompile(`(?i)url\(\s*['"]?`)

// unsafeCSS reports whether v, a value that CSS may read, as a style
// attribute, a style element's text, or a presentation attribute, could load
// or run something: a url() of anything but a fragment of the file, a
// backslash, which could escape any of these past the checks, an at-rule, as
// @import or @font-face, or one of the functions, properties, and schemes
// that load or run something, as image-set(), element(), expression(), or
// javascript:.
func unsafeCSS(v string) bool {
	lower := strings.ToLower(v)
	if strings.ContainsAny(lower, `\@`) {
		return true
	}
	// A function of CSS is its name and a parenthesis, with nothing between
	// them, so a value that names one otherwise, as class="expression", is
	// not one.
	for _, bad := range []string{"image-set(", "expression(", "src(", "element(", "javascript:", "vbscript:",
		"behavior:", "-moz-binding"} {
		if strings.Contains(lower, bad) {
			return true
		}
	}
	for _, loc := range cssURL.FindAllStringIndex(v, -1) {
		if !strings.HasPrefix(v[loc[1]:], "#") {
			return true
		}
	}
	return false
}

// xmlEscape writes s to out, escaped as the text of an element or the value
// of an attribute in double quotes.
func xmlEscape(out *bytes.Buffer, s string) {
	// EscapeText writes to a buffer without failing.
	_ = xml.EscapeText(out, []byte(s))
}

// svgLength matches a length of SVG's width or height in user units or
// pixels, the only units that say the image's size in pixels.
var svgLength = regexp.MustCompile(`^\s*([0-9]*\.?[0-9]+(?:[eE][+-]?[0-9]+)?)\s*(px)?\s*$`)

// svgSize returns the size of the SVG whose root element has the attributes
// attrs, in pixels, rounded up: its width and height, or else its viewBox's,
// or 0, 0 if it says neither, or a size of more than MaxSide on a side.
func svgSize(attrs []xml.Attr) (int, int) {
	var width, height, viewBox string
	for _, a := range attrs {
		if a.Name.Space != "" {
			continue
		}
		switch a.Name.Local {
		case "width":
			width = a.Value
		case "height":
			height = a.Value
		case "viewBox":
			viewBox = a.Value
		}
	}
	w, wok := svgPixels(width)
	h, hok := svgPixels(height)
	if !wok || !hok {
		fields := strings.FieldsFunc(viewBox, func(r rune) bool {
			return r == ' ' || r == ',' || r == '\t' ||
				r == '\n' || r == '\r'
		})
		if len(fields) != 4 {
			return 0, 0
		}
		w, wok = svgPixels(fields[2])
		h, hok = svgPixels(fields[3])
		if !wok || !hok {
			return 0, 0
		}
	}
	return w, h
}

// svgPixels returns a length of SVG as whole pixels, rounded up, and true,
// or false for one that is not a positive length of at most MaxSide pixels.
func svgPixels(v string) (int, bool) {
	m := svgLength.FindStringSubmatch(v)
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil || f <= 0 || f > MaxSide {
		return 0, false
	}
	n := int(f)
	if float64(n) < f {
		n++
	}
	return n, true
}
