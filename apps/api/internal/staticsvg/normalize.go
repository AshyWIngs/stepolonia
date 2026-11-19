// Package staticsvg validates bounded SVG drawings for use as static images.
package staticsvg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	MaxBytes           = 4096
	svgNamespace       = "http://www.w3.org/2000/svg"
	shortSVGNamespace  = "http://w3.org"
	svgWhitespace      = " \t\r\n"
	svgRootElement     = "svg"
	svgPolygonElement  = "polygon"
	svgPointsAttribute = "points"
	maxIconCoordinates = 512
)

var (
	ErrInvalid        = errors.New("invalid static SVG")
	iconNumberPattern = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?`)
	iconColorPattern  = regexp.MustCompile(`^(?:[A-Za-z]{3,24}|#[0-9a-fA-F]{3}|#[0-9a-fA-F]{6})$`)
)

var iconGeometry = map[string]string{
	"path": "d", "rect": "x y width height rx ry", "circle": "cx cy r", "ellipse": "cx cy rx ry",
	"line": "x1 y1 x2 y2", svgPolygonElement: svgPointsAttribute, "polyline": svgPointsAttribute,
}

// Normalize returns a static SVG document containing only bounded shapes.
func Normalize(source string) (string, error) {
	if len(source) > MaxBytes {
		return "", ErrInvalid
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return "", nil
	}
	var result bytes.Buffer
	decoder := xml.NewDecoder(strings.NewReader(source))
	drawing := iconDocument{encoder: xml.NewEncoder(&result)}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil || !drawing.accept(token) {
			return "", ErrInvalid
		}
	}
	if !drawing.hasShape || len(drawing.stack) != 0 || drawing.encoder.Flush() != nil || result.Len() > MaxBytes {
		return "", ErrInvalid
	}
	return result.String(), nil
}

type iconDocument struct {
	encoder  *xml.Encoder
	stack    []string
	nodes    int
	hasShape bool
}

func (d *iconDocument) accept(token xml.Token) bool {
	switch value := token.(type) {
	case xml.StartElement:
		return d.start(value)
	case xml.EndElement:
		d.stack = d.stack[:len(d.stack)-1]
		return d.encoder.EncodeToken(xml.EndElement{Name: xml.Name{Local: value.Name.Local}}) == nil
	case xml.CharData:
		return len(bytes.TrimSpace(value)) == 0
	case xml.Comment:
		return true
	default:
		return false
	}
}

func (d *iconDocument) start(element xml.StartElement) bool {
	d.nodes++
	if d.nodes > 64 || len(d.stack) >= 8 || !allowedIconElement(element, d.stack, d.nodes) {
		return false
	}
	start, ok := iconElement(element, len(d.stack) == 0)
	if !ok || d.encoder.EncodeToken(start) != nil {
		return false
	}
	d.stack = append(d.stack, element.Name.Local)
	_, geometric := iconGeometry[element.Name.Local]
	d.hasShape = d.hasShape || geometric
	return true
}

func allowedIconElement(element xml.StartElement, stack []string, count int) bool {
	name := element.Name
	if name.Space != "" && !supportedSVGNamespace(name.Space) {
		return false
	}
	if len(stack) == 0 {
		return count == 1 && name.Local == svgRootElement
	}
	parent := stack[len(stack)-1]
	_, shape := iconGeometry[name.Local]
	return (parent == svgRootElement || parent == "g") && (name.Local == "g" || shape)
}

func iconElement(element xml.StartElement, root bool) (xml.StartElement, bool) {
	name := element.Name.Local
	out := xml.StartElement{Name: xml.Name{Local: name}}
	if root {
		out.Attr = append(out.Attr, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: svgNamespace})
	}
	seen := make(map[string]bool, len(element.Attr))
	for _, attr := range element.Attr {
		key := attr.Name.Local
		if attr.Name.Space != "" || seen[key] {
			return out, false
		}
		seen[key] = true
		if root && key == "xmlns" && supportedSVGNamespace(attr.Value) {
			continue
		}
		attr.Value = strings.TrimSpace(attr.Value)
		if !validIconAttribute(name, key, attr.Value) {
			return out, false
		}
		out.Attr = append(out.Attr, attr)
	}
	return out, iconRequiredAttributes(name, seen)
}

func supportedSVGNamespace(namespace string) bool {
	return namespace == svgNamespace || namespace == shortSVGNamespace
}

func iconRequiredAttributes(element string, seen map[string]bool) bool {
	switch element {
	case svgRootElement:
		return seen["viewBox"]
	case "path":
		return seen["d"]
	case "polyline", svgPolygonElement:
		return seen[svgPointsAttribute]
	case "rect":
		return seen["width"] && seen["height"]
	case "circle":
		return seen["r"]
	case "ellipse":
		return seen["rx"] && seen["ry"]
	default:
		return true
	}
}

func validIconAttribute(element, key, value string) bool {
	if validIconPaint(key, value) {
		return true
	}
	if element == svgRootElement {
		return validIconViewport(key, value)
	}
	if !strings.Contains(" "+iconGeometry[element]+" ", " "+key+" ") {
		return false
	}
	switch key {
	case "d":
		return validIconPath(value)
	case svgPointsAttribute:
		points := iconNumbers(value)
		minimum := 4
		if element == svgPolygonElement {
			minimum = 6
		}
		return len(points) >= minimum && len(points)%2 == 0
	default:
		number, ok := iconNumber(value)
		switch key {
		case "width", "height", "r", "rx", "ry":
			return ok && number >= 0
		default:
			return ok
		}
	}
}

func validIconViewport(key, value string) bool {
	if key == "viewBox" {
		box := iconNumbers(value)
		return len(box) == 4 && box[2] > 0 && box[3] > 0
	}
	number, ok := iconNumber(value)
	return (key == "width" || key == "height") && ok && number > 0
}

func validIconPaint(key, value string) bool {
	switch key {
	case "fill", "stroke":
		return iconColorPattern.MatchString(value)
	case "fill-rule":
		return value == "nonzero" || value == "evenodd"
	case "stroke-linecap":
		return value == "butt" || value == "round" || value == "square"
	case "stroke-linejoin":
		return value == "miter" || value == "round" || value == "bevel"
	case "stroke-width":
		number, ok := iconNumber(value)
		return ok && number >= 0
	case "opacity", "fill-opacity", "stroke-opacity":
		number, ok := iconNumber(value)
		return ok && number >= 0 && number <= 1
	default:
		return false
	}
}

func validIconPath(value string) bool {
	if len(value) < 2 || len(value) > 2048 || (value[0] != 'm' && value[0] != 'M') {
		return false
	}
	total := 0
	for value != "" {
		command := value[0]
		arity := iconPathArity(command)
		if arity < 0 {
			return false
		}
		value = strings.TrimLeft(value[1:], svgWhitespace)
		if arity == 0 {
			continue
		}
		rest, count, ok := iconPathArguments(value, command, arity)
		total += count
		if !ok || total > maxIconCoordinates {
			return false
		}
		value = rest
	}
	return true
}

func iconPathArity(command byte) int {
	switch command {
	case 'm', 'M', 'l', 'L', 't', 'T':
		return 2
	case 'h', 'H', 'v', 'V':
		return 1
	case 'c', 'C':
		return 6
	case 's', 'S', 'q', 'Q':
		return 4
	case 'a', 'A':
		return 7
	case 'z', 'Z':
		return 0
	default:
		return -1
	}
}

func iconPathArguments(value string, command byte, arity int) (string, int, bool) {
	for count := 0; count < maxIconCoordinates; count++ {
		if count > 0 && count%arity == 0 && (value == "" || iconPathArity(value[0]) >= 0) {
			return value, count, true
		}
		if count > 0 {
			value = strings.TrimLeft(strings.TrimPrefix(value, ","), svgWhitespace)
		}
		rest, ok := iconPathOperand(value, command, count%arity)
		if !ok {
			return "", 0, false
		}
		value = strings.TrimLeft(rest, svgWhitespace)
	}
	return value, maxIconCoordinates, maxIconCoordinates%arity == 0
}

func iconPathOperand(value string, command byte, index int) (string, bool) {
	arc := command == 'a' || command == 'A'
	if arc && (index == 3 || index == 4) {
		// The two arc flags each consume one digit, including in compact paths.
		if value == "" || (value[0] != '0' && value[0] != '1') {
			return "", false
		}
		return value[1:], true
	}
	operand := iconNumberPattern.FindString(value)
	number, ok := boundedIconNumber(operand)
	if arc && index < 2 && number < 0 {
		return "", false
	}
	return value[len(operand):], ok
}

func iconNumbers(value string) []float64 {
	var numbers []float64
	for value != "" {
		operand := iconNumberPattern.FindString(value)
		number, ok := boundedIconNumber(operand)
		if !ok || len(numbers) == maxIconCoordinates {
			return nil
		}
		numbers = append(numbers, number)
		rest := value[len(operand):]
		if rest == "" {
			break
		}
		value = strings.TrimLeft(rest, svgWhitespace)
		value = strings.TrimLeft(strings.TrimPrefix(value, ","), svgWhitespace)
		if value == "" || value == rest {
			return nil
		}
	}
	return numbers
}

func iconNumber(value string) (float64, bool) {
	if iconNumberPattern.FindString(value) != value {
		return 0, false
	}
	return boundedIconNumber(value)
}

func boundedIconNumber(value string) (float64, bool) {
	number, err := strconv.ParseFloat(value, 64)
	return number, err == nil && math.Abs(number) <= 8192
}
