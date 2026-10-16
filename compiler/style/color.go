package style

import (
	"math"
	"strconv"
	"strings"
)

// rgb is a color in sRGB, each channel from 0 to 1.
type rgb struct{ r, g, b float64 }

// ParseColor parses a CSS color: #rgb, #rgba, #rrggbb, #rrggbbaa, rgb(),
// rgba(), hsl(), hsla(), oklch() or a named color. Alpha is ignored.
func ParseColor(s string) (rgb, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(s, "#") {
		return parseHex(s[1:])
	}
	if hex, ok := namedColors[s]; ok {
		return parseHex(hex)
	}
	open := strings.IndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		return rgb{}, false
	}
	fn, args := s[:open], colorArgs(s[open+1:len(s)-1])
	if len(args) < 3 {
		return rgb{}, false
	}
	switch fn {
	case "rgb", "rgba":
		var c [3]float64
		for i := range c {
			v, pct, ok := number(args[i])
			if !ok {
				return rgb{}, false
			}
			if pct {
				c[i] = v / 100
			} else {
				c[i] = v / 255
			}
		}
		return rgb{clamp(c[0]), clamp(c[1]), clamp(c[2])}, true
	case "hsl", "hsla":
		h, _, ok1 := number(strings.TrimSuffix(args[0], "deg"))
		sat, _, ok2 := number(args[1])
		l, _, ok3 := number(args[2])
		if !ok1 || !ok2 || !ok3 {
			return rgb{}, false
		}
		return hslToRGB(h, sat/100, l/100), true
	case "oklch":
		l, pct, ok1 := number(args[0])
		c, _, ok2 := number(args[1])
		h, _, ok3 := number(strings.TrimSuffix(args[2], "deg"))
		if !ok1 || !ok2 || !ok3 {
			return rgb{}, false
		}
		if pct {
			l /= 100
		}
		return oklchToRGB(l, c, h), true
	}
	return rgb{}, false
}

// colorArgs splits a color function's arguments on commas, spaces and
// the alpha slash.
func colorArgs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '/' || r == '\t' })
}

func number(s string) (v float64, pct bool, ok bool) {
	if strings.HasSuffix(s, "%") {
		s, pct = s[:len(s)-1], true
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, pct, err == nil
}

func parseHex(h string) (rgb, bool) {
	switch len(h) {
	case 3, 4:
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	case 6, 8:
		h = h[:6]
	default:
		return rgb{}, false
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return rgb{}, false
	}
	return rgb{float64(n>>16&0xff) / 255, float64(n>>8&0xff) / 255, float64(n&0xff) / 255}, true
}

func hslToRGB(h, s, l float64) rgb {
	h = math.Mod(math.Mod(h, 360)+360, 360) / 360
	f := func(n float64) float64 {
		k := math.Mod(n+h*12, 12)
		a := s * math.Min(l, 1-l)
		return l - a*math.Max(-1, math.Min(math.Min(k-3, 9-k), 1))
	}
	return rgb{clamp(f(0)), clamp(f(8)), clamp(f(4))}
}

func oklchToRGB(l, c, h float64) rgb {
	a, b := c*math.Cos(h*math.Pi/180), c*math.Sin(h*math.Pi/180)
	l_ := l + 0.3963377774*a + 0.2158037573*b
	m_ := l - 0.1055613458*a - 0.0638541728*b
	s_ := l - 0.0894841775*a - 1.2914855480*b
	L, M, S := l_*l_*l_, m_*m_*m_, s_*s_*s_
	lin := [3]float64{
		+4.0767416621*L - 3.3077115913*M + 0.2309699292*S,
		-1.2684380046*L + 2.6097574011*M - 0.3413193965*S,
		-0.0041960863*L - 0.7034186147*M + 1.7076147010*S,
	}
	var out [3]float64
	for i, v := range lin {
		if v <= 0.0031308 {
			out[i] = 12.92 * v
		} else {
			out[i] = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
	}
	return rgb{clamp(out[0]), clamp(out[1]), clamp(out[2])}
}

func clamp(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// luminance is WCAG relative luminance.
func (c rgb) luminance() float64 {
	lin := func(v float64) float64 {
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b)
}

func (c rgb) distance(d rgb) float64 {
	dr, dg, db := c.r-d.r, c.g-d.g, c.b-d.b
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

// ContrastRatio returns the WCAG contrast ratio of two colors, from 1 to
// 21, or 0 if either isn't a color.
func ContrastRatio(a, b string) float64 {
	ca, ok1 := ParseColor(a)
	cb, ok2 := ParseColor(b)
	if !ok1 || !ok2 {
		return 0
	}
	la, lb := ca.luminance(), cb.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// lengthPx converts a length to pixels, counting rem and em as 16px.
func lengthPx(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.' || s[i] == '-' || s[i] == '+') {
		i++
	}
	if i == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(s[i:]) {
	case "px":
		return v, true
	case "rem", "em":
		return v * 16, true
	case "pt":
		return v * 4 / 3, true
	}
	if lengthUnits[strings.ToLower(s[i:])] {
		return v, true // a unit with no fixed size; good enough to rank
	}
	return 0, false
}

// isZero reports whether s is a zero number, which needs no unit or token.
func isZero(s string) bool {
	v, err := strconv.ParseFloat(strings.TrimRight(s, "abcdefghijklmnopqrstuvwxyz%"), 64)
	return err == nil && v == 0
}

func isDuration(s string) bool {
	for _, unit := range []string{"ms", "s"} {
		if strings.HasSuffix(s, unit) {
			_, err := strconv.ParseFloat(strings.TrimSuffix(s, unit), 64)
			return err == nil
		}
	}
	return false
}

// lengthUnits are the CSS length units. Percentages and fr are not
// lengths: they are layout values, which need no token.
var lengthUnits = map[string]bool{
	"px": true, "rem": true, "em": true, "ex": true, "ch": true, "cap": true, "ic": true, "lh": true, "rlh": true,
	"vw": true, "vh": true, "vmin": true, "vmax": true, "vi": true, "vb": true, "svh": true, "lvh": true, "dvh": true,
	"svw": true, "lvw": true, "dvw": true, "cqw": true, "cqh": true, "cqi": true, "cqb": true, "cqmin": true, "cqmax": true,
	"pt": true, "pc": true, "cm": true, "mm": true, "q": true, "in": true,
}

// namedColors are the CSS named colors.
var namedColors = map[string]string{
	"aliceblue": "f0f8ff", "antiquewhite": "faebd7", "aqua": "00ffff", "aquamarine": "7fffd4", "azure": "f0ffff",
	"beige": "f5f5dc", "bisque": "ffe4c4", "black": "000000", "blanchedalmond": "ffebcd", "blue": "0000ff",
	"blueviolet": "8a2be2", "brown": "a52a2a", "burlywood": "deb887", "cadetblue": "5f9ea0", "chartreuse": "7fff00",
	"chocolate": "d2691e", "coral": "ff7f50", "cornflowerblue": "6495ed", "cornsilk": "fff8dc", "crimson": "dc143c",
	"cyan": "00ffff", "darkblue": "00008b", "darkcyan": "008b8b", "darkgoldenrod": "b8860b", "darkgray": "a9a9a9",
	"darkgreen": "006400", "darkgrey": "a9a9a9", "darkkhaki": "bdb76b", "darkmagenta": "8b008b", "darkolivegreen": "556b2f",
	"darkorange": "ff8c00", "darkorchid": "9932cc", "darkred": "8b0000", "darksalmon": "e9967a", "darkseagreen": "8fbc8f",
	"darkslateblue": "483d8b", "darkslategray": "2f4f4f", "darkslategrey": "2f4f4f", "darkturquoise": "00ced1",
	"darkviolet": "9400d3", "deeppink": "ff1493", "deepskyblue": "00bfff", "dimgray": "696969", "dimgrey": "696969",
	"dodgerblue": "1e90ff", "firebrick": "b22222", "floralwhite": "fffaf0", "forestgreen": "228b22", "fuchsia": "ff00ff",
	"gainsboro": "dcdcdc", "ghostwhite": "f8f8ff", "gold": "ffd700", "goldenrod": "daa520", "gray": "808080",
	"green": "008000", "greenyellow": "adff2f", "grey": "808080", "honeydew": "f0fff0", "hotpink": "ff69b4",
	"indianred": "cd5c5c", "indigo": "4b0082", "ivory": "fffff0", "khaki": "f0e68c", "lavender": "e6e6fa",
	"lavenderblush": "fff0f5", "lawngreen": "7cfc00", "lemonchiffon": "fffacd", "lightblue": "add8e6", "lightcoral": "f08080",
	"lightcyan": "e0ffff", "lightgoldenrodyellow": "fafad2", "lightgray": "d3d3d3", "lightgreen": "90ee90", "lightgrey": "d3d3d3",
	"lightpink": "ffb6c1", "lightsalmon": "ffa07a", "lightseagreen": "20b2aa", "lightskyblue": "87cefa", "lightslategray": "778899",
	"lightslategrey": "778899", "lightsteelblue": "b0c4de", "lightyellow": "ffffe0", "lime": "00ff00", "limegreen": "32cd32",
	"linen": "faf0e6", "magenta": "ff00ff", "maroon": "800000", "mediumaquamarine": "66cdaa", "mediumblue": "0000cd",
	"mediumorchid": "ba55d3", "mediumpurple": "9370db", "mediumseagreen": "3cb371", "mediumslateblue": "7b68ee",
	"mediumspringgreen": "00fa9a", "mediumturquoise": "48d1cc", "mediumvioletred": "c71585", "midnightblue": "191970",
	"mintcream": "f5fffa", "mistyrose": "ffe4e1", "moccasin": "ffe4b5", "navajowhite": "ffdead", "navy": "000080",
	"oldlace": "fdf5e6", "olive": "808000", "olivedrab": "6b8e23", "orange": "ffa500", "orangered": "ff4500",
	"orchid": "da70d6", "palegoldenrod": "eee8aa", "palegreen": "98fb98", "paleturquoise": "afeeee", "palevioletred": "db7093",
	"papayawhip": "ffefd5", "peachpuff": "ffdab9", "peru": "cd853f", "pink": "ffc0cb", "plum": "dda0dd",
	"powderblue": "b0e0e6", "purple": "800080", "rebeccapurple": "663399", "red": "ff0000", "rosybrown": "bc8f8f",
	"royalblue": "4169e1", "saddlebrown": "8b4513", "salmon": "fa8072", "sandybrown": "f4a460", "seagreen": "2e8b57",
	"seashell": "fff5ee", "sienna": "a0522d", "silver": "c0c0c0", "skyblue": "87ceeb", "slateblue": "6a5acd",
	"slategray": "708090", "slategrey": "708090", "snow": "fffafa", "springgreen": "00ff7f", "steelblue": "4682b4",
	"tan": "d2b48c", "teal": "008080", "thistle": "d8bfd8", "tomato": "ff6347", "turquoise": "40e0d0",
	"violet": "ee82ee", "wheat": "f5deb3", "white": "ffffff", "whitesmoke": "f5f5f5", "yellow": "ffff00",
	"yellowgreen": "9acd32",
}
