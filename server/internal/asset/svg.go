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
// only the elements that an allowlist names, and of their attributes only
// those that can neither run nor load anything, so that a file that the
// server stores holds no script and refers to nothing outside itself, and it
// never stores the file as it was uploaded. It keeps:
//
//   - the elements of SVG's namespace that draw, style, or structure an
//     image (svgElements), with their text, and drops any other element with
//     everything inside it: a script, a foreignObject, an animation, which
//     could set an attribute to a script's URL, or an element of another
//     namespace, as an editor's own;
//   - the attributes of no namespace whose names are names, and xml:space and
//     xml:lang, but not an event handler, whose name starts with "on", nor
//     one whose value names a URL outside the file, with a scheme, as
//     https:, or a host, as //host, as ping's would, which a browser could
//     send a request to (namesURL);
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
// are text, as Illustrator declares the namespaces and the styles with, so
// that a file that refers to an external entity, or nests entities, is
// refused, and it refuses a file whose references to its entities would
// expand to more than maxSVGExpansion bytes in all, which the decoder would
// otherwise expand in memory: a file of 3 MB holds a million references to an
// entity of a kilobyte, a gigabyte in all. A file has one document type at
// most, as XML allows, so that the references are counted once, rather than
// once for each of a million declarations that a file of a few megabytes
// could hold. The file must be UTF-8, and its root an svg element of SVG's
// namespace. A file that is well-formed, but that goes past one of these
// bounds, or those of its elements' depth and attributes, is refused as
// ReasonUnsupported, rather than as a damaged one.
//
// What it writes may take more bytes than what it read, as a character that
// a section of CDATA holds as it is, & or <, is written as a reference, and
// it takes at most as many as the caller says, the most that an upload may
// store, so that a small file cannot make the server hold, or store, a huge
// one: a larger one is refused with ErrTooLarge.

const (
	svgNamespace   = "http://www.w3.org/2000/svg"
	xlinkNamespace = "http://www.w3.org/1999/xlink"
	xmlNamespace   = "http://www.w3.org/XML/1998/namespace"
)

// maxSVGDepth bounds how deeply an SVG's elements nest.
const maxSVGDepth = 256

// maxSVGAttrs bounds how many attributes an element may have, its namespace
// declarations among them, and how many namespaces the elements that are
// open at once may declare in all. The decoder reads all of an element's
// attributes into memory at once, each of which takes many times the bytes
// that it takes in the file, before the sanitizer sees any of them, so the
// file is read for them first, and refused before the decoder reads it if an
// element may have too many (manyAttributes); and the decoder keeps the
// namespaces that the open elements declare until they end, so those are
// counted as they are read. So a file of a few megabytes, as an element of a
// million attributes, or elements nested 256 deep that each declare a
// thousand namespaces, cannot make the server hold many times its size.
// Enough for a drawing whose elements an editor wrote with many, and little
// next to what a file of a few megabytes holds.
const maxSVGAttrs = 1024

// The bounds of the entities that an SVG's document type may declare, and of
// the bytes that the references to them may expand to in all: enough for a
// drawing whose every element takes its style from an entity, as Illustrator
// writes one, and little next to what an upload may hold.
const (
	maxSVGEntities      = 64
	maxSVGEntityLength  = 1024
	maxSVGEntityNameLen = 64
	maxSVGExpansion     = 4 << 20
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
// SVG's namespace, ReasonMalformed if it is, or its first element is an svg
// element (startsAsSVG), but it is not well-formed XML in UTF-8, and
// ReasonUnsupported if it is well-formed, but declares another encoding, or
// another version of XML, or goes past one of the bounds of its elements' depth and attributes, or of
// its entities, or ErrTooLarge if the image, sanitized, would take more than
// limit bytes.
func sanitizeSVG(data []byte, limit int) (Image, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		if startsAsSVG(data) {
			return Image{}, &ImageError{Reason: ReasonMalformed, Detail: "it is an SVG that is not in UTF-8"}
		}
		return Image{}, errNotImage
	}
	if manyAttributes(data) {
		if !startsAsSVG(data) {
			return Image{}, errNotImage
		}
		return Image{}, unsupported(fmt.Sprintf("an element has more than %d attributes, as the equals signs "+
			"that follow its tag count them", maxSVGAttrs))
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	// A charset other than UTF-8 is refused: the decoder has no reader of it.
	d.CharsetReader = nil
	d.Entity = map[string]string{}

	out := &svgOut{limit: limit, seen: map[string]bool{}}
	out.write(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	var img Image
	depth, skip := 0, 0
	rootSeen, rootDone, doctype := false, false, false
	// declared is how many namespaces the open elements declare, which the
	// decoder keeps until they end, and declaredAt how many the open element
	// at each depth declares.
	declared := 0
	var declaredAt [maxSVGDepth + 1]int
	// inStyle is set inside a style element that is kept, whose text is CSS.
	inStyle := false
	var styleText strings.Builder
	for {
		if out.full {
			return Image{}, ErrTooLarge
		}
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if !rootSeen && !startsAsSVG(data) {
				return Image{}, errNotImage
			}
			// The decoder's errors of the file's syntax are *xml.SyntaxError, and
			// those of an XML declaration that names a version other than 1.0,
			// or an encoding that it has no reader of, as ISO-8859-1, are not.
			if _, syntax := errors.AsType[*xml.SyntaxError](err); !syntax {
				return Image{}, unsupported("it is an SVG whose XML declaration names a version other than 1.0, " +
					"or an encoding other than UTF-8")
			}
			return Image{}, &ImageError{Reason: ReasonMalformed,
				Detail: "it is an SVG that is not well-formed XML in UTF-8"}
		}
		switch t := tok.(type) {
		case xml.Directive:
			switch {
			case rootSeen:
				return Image{}, &ImageError{Reason: ReasonMalformed, Detail: "it has a document type inside its root"}
			case doctype:
				return Image{}, &ImageError{Reason: ReasonMalformed, Detail: "it has more than one document type"}
			}
			doctype = true
			if err := declareEntities(d, t, data); err != nil {
				return Image{}, err
			}
		case xml.StartElement:
			if rootDone {
				return Image{}, &ImageError{Reason: ReasonMalformed, Detail: "it has more than one root element"}
			}
			depth++
			if depth > maxSVGDepth {
				return Image{}, unsupported(fmt.Sprintf("its elements nest more than %d deep", maxSVGDepth))
			}
			declaredAt[depth] = namespaces(t.Attr)
			if declared += declaredAt[depth]; declared > maxSVGAttrs {
				return Image{}, unsupported(fmt.Sprintf("the elements that enclose one of its elements declare "+
					"more than %d namespaces in all", maxSVGAttrs))
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
			writeStart(out, t, depth == 1)
			if t.Name.Local == "style" {
				inStyle = true
				styleText.Reset()
			}
		case xml.EndElement:
			declared -= declaredAt[depth]
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
					out.escape(css, false)
				}
			}
			out.write("</" + t.Name.Local + ">")
		case xml.CharData:
			switch {
			case skip > 0 || depth == 0:
			case inStyle:
				styleText.Write(t)
			default:
				out.escape(string(t), false)
			}
		}
	}
	switch {
	case !rootSeen:
		return Image{}, errNotImage
	case out.full:
		return Image{}, ErrTooLarge
	}
	img.Mime, img.Ext, img.Data = MimeSVG, extensions[MimeSVG], out.Bytes()
	return img, nil
}

// svgOut is the file that sanitizeSVG writes, which takes at most limit
// bytes: once a write would take it past them, full is set, and nothing more
// is written.
type svgOut struct {
	bytes.Buffer
	limit int
	full  bool
	// seen holds the names of the attributes that writeStart has written of
	// the element that it writes, which the decoder does not keep from
	// repeating, and which it empties for each element, rather than make a
	// map for each.
	seen map[string]bool
}

// unsupported returns the *ImageError of an SVG file that is well-formed,
// but that the server does not take, for the reason detail.
func unsupported(detail string) *ImageError {
	return &ImageError{Reason: ReasonUnsupported, Detail: detail}
}

// manyAttributes reports whether data may hold an element of more than
// maxSVGAttrs attributes, before the decoder reads any: whether more than
// maxSVGAttrs equals signs follow a less-than sign before the next. Each
// attribute of a start tag has an equals sign, which the decoder requires,
// and no attribute's value has a less-than sign, which the decoder refuses
// there, so the equals signs from the less-than sign that opens a tag to the
// next count every attribute of the tag, and those of the text that follows
// it, which a drawing has few of, as of a comment or a style element's CSS.
// It reads data once.
func manyAttributes(data []byte) bool {
	for rest := data; ; {
		start := bytes.IndexByte(rest, '<')
		if start < 0 {
			return false
		}
		rest = rest[start+1:]
		end := bytes.IndexByte(rest, '<')
		if end < 0 {
			end = len(rest)
		}
		if bytes.Count(rest[:end], []byte("=")) > maxSVGAttrs {
			return true
		}
		rest = rest[end:]
	}
}

// namespaces returns how many of attrs, the attributes of an element that
// the decoder returned, declare a namespace, with a prefix or the default.
func namespaces(attrs []xml.Attr) int {
	n := 0
	for _, a := range attrs {
		if a.Name.Space == "xmlns" || a.Name.Space == "" && a.Name.Local == "xmlns" {
			n++
		}
	}
	return n
}

// write writes s, unless it would take the file past its limit.
func (o *svgOut) write(s string) {
	if o.full || o.Len()+len(s) > o.limit {
		o.full = true
		return
	}
	o.WriteString(s)
}

// escape writes s, escaped as the text of an element, or, if attr is set, as
// the value of an attribute in double quotes: &, <, and >, and a carriage
// return, which a parser would read as a line feed, as references, and, in
// an attribute's value, a double quote, and a line feed or a tab, which a
// parser would read as a space, and the rest as it is, so that text that
// spans lines, as between the elements of a file that an editor indented,
// takes no more bytes than it did.
func (o *svgOut) escape(s string, attr bool) {
	last := 0
	for i := 0; i < len(s) && !o.full; i++ {
		var ref string
		switch c := s[i]; {
		case c == '&':
			ref = "&amp;"
		case c == '<':
			ref = "&lt;"
		case c == '>':
			ref = "&gt;"
		case c == '\r':
			ref = "&#xD;"
		case attr && c == '"':
			ref = "&quot;"
		case attr && c == '\n':
			ref = "&#xA;"
		case attr && c == '\t':
			ref = "&#x9;"
		default:
			continue
		}
		o.write(s[last:i])
		o.write(ref)
		last = i + 1
	}
	o.write(s[last:])
}

// firstElement matches the start of the tag of an element, and captures its
// name: a less-than sign and a letter or an underscore, which neither a
// declaration, as <!DOCTYPE, nor a processing instruction, as <?xml, starts
// with.
var firstElement = regexp.MustCompile(`<([A-Za-z_][A-Za-z0-9_.:-]*)`)

// startsAsSVG reports whether the first element of data, as far as text that
// may not be XML shows it, is an svg element, with a prefix or without, as a
// file is whose root an SVG editor wrote, so that such a file that is not
// well-formed is refused as an SVG that is damaged, rather than as a file
// that is none of the types.
func startsAsSVG(data []byte) bool {
	m := firstElement.FindSubmatch(data)
	if m == nil {
		return false
	}
	name := m[1]
	if i := bytes.LastIndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	return string(name) == "svg"
}

// declareEntities adds to d's entities those that the document type dir of
// the file data declares in its internal subset with values that are text,
// or returns an *ImageError of ReasonUnsupported for a document type that
// declares more than that: an external entity, a parameter entity, an entity
// whose value refers to another, or too many or too long, or whose
// references in data would expand to more than maxSVGExpansion bytes in all.
// Everything else of the document type is ignored.
func declareEntities(d *xml.Decoder, dir xml.Directive, data []byte) error {
	text := string(dir)
	start := strings.IndexByte(text, '[')
	if start < 0 {
		return nil
	}
	subset := text[start:]
	refused := unsupported(fmt.Sprintf("its document type declares entities that the server does not read: "+
		"only %d entities at most, whose values are text of at most %d bytes, without a reference",
		maxSVGEntities, maxSVGEntityLength))
	matches := entityDecl.FindAllStringSubmatch(subset, -1)
	// A name that the subset declares twice is one entity of the decoder's,
	// so the decoder holds no more entities than the subset declares.
	if len(matches) != strings.Count(subset, "<!ENTITY") || len(matches) > maxSVGEntities {
		return refused
	}
	for _, m := range matches {
		value := m[2] + m[3]
		if len(m[1]) > maxSVGEntityNameLen || len(value) > maxSVGEntityLength {
			return refused
		}
		d.Entity[m[1]] = value
	}
	if entityExpansion(data, d.Entity) > maxSVGExpansion {
		return unsupported(fmt.Sprintf("the references to its entities expand to more than %d bytes in all",
			maxSVGExpansion))
	}
	return nil
}

// entityExpansion returns how many bytes the references in data to the
// entities expand to, or a number past maxSVGExpansion once they expand to
// more. It counts every reference that data spells, &name;, wherever it is,
// as in a comment, which the decoder does not expand, so it counts at least
// what the decoder expands, and it reads data once: a name is at most
// maxSVGEntityNameLen bytes, so it looks no further for the semicolon.
func entityExpansion(data []byte, entities map[string]string) int {
	n := 0
	for rest := data; n <= maxSVGExpansion; {
		i := bytes.IndexByte(rest, '&')
		if i < 0 {
			break
		}
		rest = rest[i+1:]
		end := bytes.IndexByte(rest[:min(len(rest), maxSVGEntityNameLen+1)], ';')
		if end < 0 {
			continue
		}
		n += len(entities[string(rest[:end])])
	}
	return n
}

// writeStart writes the start tag of the element t, which is kept, with the
// attributes that it keeps, and, on the root, the namespace of SVG.
func writeStart(out *svgOut, t xml.StartElement, root bool) {
	out.write("<" + t.Name.Local)
	if root {
		out.write(` xmlns="` + svgNamespace + `"`)
	}
	// The decoder does not refuse an attribute that an element repeats, as
	// two of href, of which the first is kept.
	clear(out.seen)
	for _, a := range t.Attr {
		name, ok := attrName(a.Name)
		if !ok || out.seen[name] || !keepAttr(t.Name.Local, name, a.Value) {
			continue
		}
		out.seen[name] = true
		out.write(" " + name + `="`)
		out.escape(a.Value, true)
		out.write(`"`)
	}
	out.write(">")
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
// element, keeps the value v: a reference, only to a fragment of the file, or
// a data: URL of an image's raster type, a style, which CSS reads, only if it
// is safe (unsafeCSS), and any other only if it is safe as CSS too, as a
// presentation attribute is CSS, and it names no URL outside the file
// (namesURL), as ping's do.
func keepAttr(element, name, v string) bool {
	switch {
	case name == "href":
		return strings.HasPrefix(v, "#") || element == "image" && dataImage.MatchString(v)
	case name != "style" && namesURL(v):
		return false
	}
	return !unsafeCSS(v)
}

// namesURL reports whether v, the value of an attribute, may be read as a URL
// of something outside the file: whether it starts as an absolute URL does,
// once the spaces, tabs, line feeds, and control characters that a browser
// drops from a URL that it parses are dropped from it, or any word of it
// does, as a list of URLs separated by spaces, as ping's, is read. A relative
// URL refers to the origin that the file is served from, whose requests reach
// nothing outside it. A value of another kind that starts with a word and a
// colon, as "Note: …" might, is dropped with them, which only words that no
// drawing needs to show it do. It reads v without copying it, as a path's
// data, of megabytes, may be.
func namesURL(v string) bool {
	if absoluteURL(v) {
		return true
	}
	for w := range strings.FieldsSeq(v) {
		if absoluteURL(w) {
			return true
		}
	}
	return false
}

// absoluteURL reports whether s, without its spaces and control characters,
// starts as a URL that says where it is does: with a scheme, as https: or
// data:, or with two slashes, as //host/path, which is relative to the scheme
// alone.
func absoluteURL(s string) bool {
	i := 0
	// next returns the next byte of s that is not a space or a control
	// character, and false at its end.
	next := func() (byte, bool) {
		for i < len(s) && s[i] <= ' ' {
			i++
		}
		if i == len(s) {
			return 0, false
		}
		i++
		return s[i-1], true
	}
	letter := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	c, ok := next()
	switch {
	case !ok:
		return false
	case c == '/':
		c, ok = next()
		return ok && c == '/'
	case !letter(c):
		return false
	}
	for {
		if c, ok = next(); !ok {
			return false
		}
		switch {
		case c == ':':
			return true
		case !letter(c) && !(c >= '0' && c <= '9') && c != '+' && c != '.' && c != '-':
			return false
		}
	}
}

// cssURL matches the start of a url() of CSS, and what follows its
// parenthesis.
var cssURL = regexp.MustCompile(`(?i)url\(\s*['"]?`)

// unsafeCSS reports whether v, a value that CSS may read, as a style
// attribute, a style element's text, or a presentation attribute, could load
// or run something: a url() of anything but a fragment of the file, a
// backslash, which could escape any of these past the checks, an at-rule, as
// @import or @font-face, or one of the functions, properties, and schemes
// that load or run something, as image-set() and image(), which take a URL as
// a string, without url(), element(), expression(), or javascript:.
func unsafeCSS(v string) bool {
	lower := strings.ToLower(v)
	if strings.ContainsAny(lower, `\@`) {
		return true
	}
	// A function of CSS is its name and a parenthesis, with nothing between
	// them, so a value that names one otherwise, as class="expression", is
	// not one.
	for _, bad := range []string{"image-set(", "image(", "expression(", "src(", "element(", "javascript:",
		"vbscript:", "behavior:", "-moz-binding"} {
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
