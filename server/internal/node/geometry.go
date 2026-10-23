package node

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/input"
)

// The bounds of a node's position and size, in units of its board's canvas,
// which the database holds them to as well (migration 00023), so that the
// undo of a change of one, which sets the stored one back, holds none that
// the payload's schema refuses: change both at once.
const (
	// MaxCoordinate is the greatest distance from the canvas's origin, either
	// way, of a node's position.
	MaxCoordinate = 1_000_000_000
	// MinLength and MaxLength are the least and the greatest width or height
	// of a node.
	MinLength = 1
	MaxLength = 100_000
)

// Coord is a coordinate of a node's position, a number from -MaxCoordinate
// to MaxCoordinate, as a payload writes it, which the node keeps to 2
// decimal places (PRD FR-EDIT-02 AC4), or none, as the zero Coord is. A
// payload's is read exactly as the decimal that it is written as, rounded
// half away from zero, so that 0.125 is 0.13 however a double would hold it,
// and the same position written as 240, 240.0, or 2.4e2 is the same, which
// is written back without its trailing zeros, as 240 (§6.2).
type Coord struct {
	raw string
}

// Length is a node's width or height, a number from MinLength to MaxLength,
// as a payload writes it, which the node keeps to 2 decimal places as it
// keeps a Coord, or none, as the zero Length is.
type Length struct {
	raw string
}

// coordSchema and lengthSchema are the schemas of a Coord and a Length.
var (
	coordSchema  = numberSchema(-MaxCoordinate, MaxCoordinate)
	lengthSchema = numberSchema(MinLength, MaxLength)
)

// numberSchema returns the schema of a number from least to most.
func numberSchema(least, most float64) *huma.Schema {
	s := &huma.Schema{Type: huma.TypeNumber, Minimum: &least, Maximum: &most}
	s.PrecomputeMessages()
	return s
}

// Schema gives huma the schema of a Coord (huma.SchemaProvider), which
// validates a command's payload and describes the field in the OpenAPI
// document.
func (Coord) Schema(huma.Registry) *huma.Schema { return coordSchema }

// Schema gives huma the schema of a Length (huma.SchemaProvider).
func (Length) Schema(huma.Registry) *huma.Schema { return lengthSchema }

// UnmarshalJSON keeps the JSON of the coordinate as it is, which a payload's
// Check reads (hundredths), as value.Value keeps a value's.
func (c *Coord) UnmarshalJSON(data []byte) error {
	c.raw = string(data)
	return nil
}

// UnmarshalJSON keeps the JSON of the length as it is, as a Coord does.
func (l *Length) UnmarshalJSON(data []byte) error {
	l.raw = string(data)
	return nil
}

// MarshalJSON writes the coordinate as it was read, a payload's as the client
// wrote it, which the operation log keeps, or as a node's position is shown,
// without trailing zeros, or null for none.
func (c Coord) MarshalJSON() ([]byte, error) { return marshalNumber(c.raw) }

// MarshalJSON writes the length as a Coord's MarshalJSON writes a
// coordinate.
func (l Length) MarshalJSON() ([]byte, error) { return marshalNumber(l.raw) }

func marshalNumber(raw string) ([]byte, error) {
	if raw == "" {
		return []byte("null"), nil
	}
	return []byte(raw), nil
}

// IsZero reports whether c is none, so that a field that a payload left out,
// tagged omitzero, stays out when the pipeline encodes the payload again.
func (c Coord) IsZero() bool { return c.raw == "" }

// IsZero reports whether l is none, as Coord.IsZero does.
func (l Length) IsZero() bool { return l.raw == "" }

// coordOf returns the coordinate of n hundredths, as a node's is shown.
func coordOf(n int64) Coord { return Coord{raw: formatHundredths(n)} }

// lengthOf returns the length of n hundredths, as a node's is shown.
func lengthOf(n int64) Length { return Length{raw: formatHundredths(n)} }

// hundredths returns the coordinate c, a payload's, in hundredths of a unit,
// rounded half away from zero, or says what is wrong with it.
func (c Coord) hundredths() (int64, string) {
	return hundredthsWithin(c.raw, -MaxCoordinate, MaxCoordinate)
}

// hundredths returns the length l, a payload's, in hundredths of a unit, as
// Coord.hundredths does.
func (l Length) hundredths() (int64, string) {
	return hundredthsWithin(l.raw, MinLength, MaxLength)
}

// hundredthsWithin returns raw, a JSON number, in hundredths of a unit,
// rounded half away from zero, if it is one from least to most once rounded,
// or says what is wrong with it.
func hundredthsWithin(raw string, least, most int64) (int64, string) {
	if !jsonNumber.MatchString(raw) {
		return 0, "is not a number"
	}
	n, problem := toHundredths(raw)
	switch {
	case problem != "":
		return 0, problem
	case n < least*100 || n > most*100:
		return 0, fmt.Sprintf("is not a number from %d to %d", least, most)
	}
	return n, ""
}

// jsonNumber is the form of a JSON number (RFC 8259), which its schema says
// that a payload's coordinate is, and which a step of an inverse writes.
var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// The bounds of how a number of a position or a size is written, as package
// value bounds a value's: no number needs more characters, or a greater
// exponent, either way, so that one that has them is refused rather than
// read at length.
const (
	maxNumberLength = 327
	maxExponent     = 999
)

// maxHundredthsDigits is how many digits a number of hundredths that a bound
// takes has at most, which leaves an int64 room.
const maxHundredthsDigits = 15

// toHundredths returns raw, a JSON number, in hundredths of a unit, rounded
// half away from zero, read exactly as the decimal that it is written as, or
// says why it is not one of maxHundredthsDigits digits at most: that it is
// written with more than maxNumberLength characters, or with an exponent
// beyond ±maxExponent, or that it is too large.
func toHundredths(raw string) (int64, string) {
	const tooLarge = "is too large for a position or a size"
	if len(raw) > maxNumberLength {
		return 0, fmt.Sprintf("is written with more than %d characters", maxNumberLength)
	}
	s, negative := strings.CutPrefix(raw, "-")
	mantissa, exponent := s, ""
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa, exponent = s[:i], s[i+1:]
	}
	e := 0
	if exponent != "" {
		digits := strings.TrimLeft(strings.TrimLeft(exponent, "+-"), "0")
		n, err := strconv.Atoi(digits)
		if digits != "" && (err != nil || n > maxExponent) {
			return 0, fmt.Sprintf("is written with an exponent beyond ±%d", maxExponent)
		}
		if strings.HasPrefix(exponent, "-") {
			n = -n
		}
		e = n
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return 0, ""
	}
	// The value is digits times ten to the power of shift, in hundredths,
	// which the bounds of the number's length and its exponent bound.
	shift := e - len(fraction) + 2
	var n int64
	if shift >= 0 {
		if len(digits)+shift > maxHundredthsDigits {
			return 0, tooLarge
		}
		n, _ = strconv.ParseInt(digits+strings.Repeat("0", shift), 10, 64)
	} else {
		keep := len(digits) + shift
		if keep > maxHundredthsDigits {
			return 0, tooLarge
		}
		// The first digit cut off rounds the rest up when it is 5 or more,
		// since what follows it can only add to it.
		first := byte('0')
		if keep >= 0 {
			first = digits[keep]
			n, _ = strconv.ParseInt("0"+digits[:keep], 10, 64)
		}
		if first >= '5' {
			n++
		}
	}
	if negative {
		n = -n
	}
	return n, ""
}

// formatHundredths writes n hundredths as a JSON number, without trailing
// zeros after its point, or without its point if it is integral, as 240,
// 120.5, or -0.05.
func formatHundredths(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	whole, cents := n/100, n%100
	switch {
	case cents == 0:
		return sign + strconv.FormatInt(whole, 10)
	case cents%10 == 0:
		return fmt.Sprintf("%s%d.%d", sign, whole, cents/10)
	}
	return fmt.Sprintf("%s%d.%02d", sign, whole, cents)
}

// numeric returns n hundredths as a column of numeric(12, 2).
func numeric(n int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(n), Exp: -2, Valid: true}
}

// nullNumeric returns n hundredths as a column of numeric(12, 2), or a null
// column if ok is false.
func nullNumeric(n int64, ok bool) pgtype.Numeric {
	if !ok {
		return pgtype.Numeric{}
	}
	return numeric(n)
}

// fromNumeric returns the column n of numeric(12, 2), which is not null, in
// hundredths of a unit.
func fromNumeric(n pgtype.Numeric) (int64, error) {
	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite || n.Int == nil {
		return 0, errors.New("node: a number of a node's position or size is not finite")
	}
	v := new(big.Int).Set(n.Int)
	ten := big.NewInt(10)
	switch e := int(n.Exp) + 2; {
	case e > 0:
		v.Mul(v, new(big.Int).Exp(ten, big.NewInt(int64(e)), nil))
	case e < 0:
		var rem big.Int
		v.QuoRem(v, new(big.Int).Exp(ten, big.NewInt(int64(-e)), nil), &rem)
		if rem.Sign() != 0 {
			return 0, errors.New("node: a number of a node's position or size has more than 2 decimal places")
		}
	}
	if !v.IsInt64() {
		return 0, errors.New("node: a number of a node's position or size is out of range")
	}
	return v.Int64(), nil
}

// NodePosition is where a node is on its board's canvas: its x, rightward,
// and its y, downward, in units of the canvas.
type NodePosition struct {
	X Coord `json:"x" doc:"The node's position across its board's canvas, rightward, in units of the canvas: a number from -1000000000 to 1000000000, read as the decimal that it is written as, and kept to 2 decimal places, rounded half away from zero, as 0.125 is 0.13 (PRD FR-EDIT-02 AC4). The node is shown with it written without trailing zeros, as 240 or 120.5."`
	Y Coord `json:"y" doc:"The node's position down its board's canvas, downward, in units of the canvas: a number from -1000000000 to 1000000000, read and kept as x is."`
}

// NodeSize is a node's width and height, in units of its board's canvas, or
// none, which the canvas then lays the node out by itself for: a payload
// sets both, or neither, as {}, for none.
type NodeSize struct {
	Width  Length `json:"width,omitzero" doc:"The node's width, in units of its board's canvas: a number from 1 to 100000, read and kept as a position's x is. With height, or neither of them, for none."`
	Height Length `json:"height,omitzero" doc:"The node's height, in units of its board's canvas: a number from 1 to 100000, read and kept as a position's x is. With width, or neither of them, for none."`
}

// checkPosition returns the problems of a payload's position at the field at:
// each coordinate is a number within the bounds.
func checkPosition(at string, p NodePosition) input.FieldErrors {
	var errs input.FieldErrors
	for _, c := range []struct {
		name  string
		coord Coord
	}{{"x", p.X}, {"y", p.Y}} {
		if _, problem := c.coord.hundredths(); problem != "" {
			errs = append(errs, input.FieldError{Field: at + "." + c.name, Message: problem})
		}
	}
	return errs
}

// checkSize returns the problems of a payload's size at the field at: both
// its width and its height, each within the bounds, or neither.
func checkSize(at string, s NodeSize) input.FieldErrors {
	switch {
	case s.Width.IsZero() && s.Height.IsZero():
		return nil
	case s.Width.IsZero():
		return field(at+".width", "is missing, which a size with a height needs")
	case s.Height.IsZero():
		return field(at+".height", "is missing, which a size with a width needs")
	}
	var errs input.FieldErrors
	for _, l := range []struct {
		name   string
		length Length
	}{{"width", s.Width}, {"height", s.Height}} {
		if _, problem := l.length.hundredths(); problem != "" {
			errs = append(errs, input.FieldError{Field: at + "." + l.name, Message: problem})
		}
	}
	return errs
}

// position returns the payload's position p, which checkPosition took, as
// columns.
func position(p NodePosition) (x, y pgtype.Numeric) {
	px, _ := p.X.hundredths()
	py, _ := p.Y.hundredths()
	return numeric(px), numeric(py)
}

// size returns the payload's size s, which checkSize took, as columns, null
// for none.
func size(s NodeSize) (width, height pgtype.Numeric) {
	if s.Width.IsZero() {
		return pgtype.Numeric{}, pgtype.Numeric{}
	}
	w, _ := s.Width.hundredths()
	h, _ := s.Height.hundredths()
	return numeric(w), numeric(h)
}

// positionOf returns the position of the columns x and y as a node's is
// shown.
func positionOf(x, y pgtype.Numeric) (NodePosition, error) {
	px, err := fromNumeric(x)
	if err != nil {
		return NodePosition{}, err
	}
	py, err := fromNumeric(y)
	if err != nil {
		return NodePosition{}, err
	}
	return NodePosition{X: coordOf(px), Y: coordOf(py)}, nil
}

// sizeOf returns the size of the columns width and height as a node's is
// shown, or nil for none.
func sizeOf(width, height pgtype.Numeric) (*NodeSize, error) {
	if !width.Valid {
		return nil, nil
	}
	w, err := fromNumeric(width)
	if err != nil {
		return nil, err
	}
	h, err := fromNumeric(height)
	if err != nil {
		return nil, err
	}
	return &NodeSize{Width: lengthOf(w), Height: lengthOf(h)}, nil
}

// sameNumeric reports whether the columns a and b hold the same number, or
// are both null.
func sameNumeric(a, b pgtype.Numeric) bool {
	if a.Valid != b.Valid {
		return false
	}
	if !a.Valid {
		return true
	}
	x, errA := fromNumeric(a)
	y, errB := fromNumeric(b)
	return errA == nil && errB == nil && x == y
}
