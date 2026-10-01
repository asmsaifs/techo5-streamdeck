package render

import (
	"fmt"
	"image/color"
)

// ParseColour reads #rgb or #rrggbb, the forms the config validates.
func ParseColour(s string) (color.RGBA, error) {
	var r, g, b uint8
	switch len(s) {
	case 4:
		if _, err := fmt.Sscanf(s, "#%1x%1x%1x", &r, &g, &b); err != nil {
			return color.RGBA{}, fmt.Errorf("%q is not a colour", s)
		}
		r, g, b = r*17, g*17, b*17
	case 7:
		if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil {
			return color.RGBA{}, fmt.Errorf("%q is not a colour", s)
		}
	default:
		return color.RGBA{}, fmt.Errorf("%q is not a colour", s)
	}
	return color.RGBA{r, g, b, 255}, nil
}

// mix is a blended with b: t is how much of b, 0 to 1. Both are opaque.
func mix(a, b color.RGBA, t float64) color.RGBA {
	f := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.RGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 255}
}

func hex(c color.RGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
