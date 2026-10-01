package render

import (
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // icons may be JPEGs
	_ "image/png"  // and PNGs
	"os"
	"path/filepath"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	xdraw "golang.org/x/image/draw"
)

// lucide holds the Lucide icon set (ISC licence, see lucide/LICENSE). Each file is only the
// shapes inside the <svg> element: the wrapper is the same for all 2000 and is added on load.
//
//go:embed lucide/*.svg
var lucide embed.FS

const (
	lucidePrefix = "lucide:"
	// fallbackIcon stands in for an icon that cannot be drawn: an unknown name, a missing file,
	// an emoji (which needs a colour font the renderer does not have yet).
	fallbackIcon = lucidePrefix + "circle-help"
	maxIconCache = 256
)

type iconKey struct {
	name string
	size int
	col  color.RGBA // zero for an image, which keeps its own colours
	mod  int64      // the file's modification time, so an edited icon is picked up
}

// icon is name drawn size pixels square, in col if it is a line icon. It never fails: what it
// cannot draw it replaces with fallbackIcon.
func (r *Renderer) icon(name string, size int, col color.RGBA) *image.RGBA {
	if size < 1 {
		return nil
	}
	key := iconKey{name: name, size: size}
	var load func() (*image.RGBA, error)
	switch {
	case strings.HasPrefix(name, lucidePrefix):
		key.col = col
		load = func() (*image.RGBA, error) { return rasterLucide(strings.TrimPrefix(name, lucidePrefix), size, col) }
	case isImageName(name):
		fi, err := os.Stat(filepath.Join(r.iconsDir, name))
		if err != nil {
			return r.icon(fallbackIcon, size, col)
		}
		key.mod = fi.ModTime().UnixNano()
		load = func() (*image.RGBA, error) { return loadImage(filepath.Join(r.iconsDir, name), size) }
	default:
		return r.icon(fallbackIcon, size, col)
	}

	r.mu.Lock()
	img, ok := r.icons[key]
	r.mu.Unlock()
	if ok {
		return img
	}
	img, err := load()
	if err != nil {
		if name == fallbackIcon {
			return nil
		}
		return r.icon(fallbackIcon, size, col)
	}
	r.mu.Lock()
	if len(r.icons) >= maxIconCache {
		clear(r.icons) // few icons are ever on screen; a flush is cheaper than an LRU
	}
	r.icons[key] = img
	r.mu.Unlock()
	return img
}

// isImageName reports whether name is a plain file name with an image extension. A path is not
// one: icons are only read from the icons directory.
func isImageName(name string) bool {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}

func rasterLucide(name string, size int, col color.RGBA) (*image.RGBA, error) {
	if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return nil, fmt.Errorf("%q is not a lucide icon name", name)
	}
	body, err := lucide.ReadFile("lucide/" + name + ".svg")
	if err != nil {
		return nil, fmt.Errorf("no lucide icon %q", name)
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"` +
		` stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + string(body) + `</svg>`
	ic, err := oksvg.ReadReplacingCurrentColor(strings.NewReader(svg), hex(col))
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	ic.SetTarget(0, 0, float64(size), float64(size))
	ic.Draw(rasterx.NewDasher(size, size, rasterx.NewScannerGV(size, size, img, img.Bounds())), 1)
	return img, nil
}

// loadImage reads a PNG or JPEG and fits it inside a size by size square, keeping its shape.
func loadImage(path string, size int) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	b := src.Bounds()
	w, h := size, size
	if b.Dx() > b.Dy() {
		h = max(1, b.Dy()*size/b.Dx())
	} else {
		w = max(1, b.Dx()*size/b.Dy())
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	at := image.Rect((size-w)/2, (size-h)/2, (size-w)/2+w, (size-h)/2+h)
	xdraw.CatmullRom.Scale(img, at, src, b, draw.Over, nil)
	return img, nil
}

// LucideNames lists the bundled line icons as "lucide:name", sorted.
func LucideNames() []string {
	ents, _ := lucide.ReadDir("lucide")
	var out []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".svg"); ok {
			out = append(out, lucidePrefix+n)
		}
	}
	return out
}

// LucideSVG is the bundled icon name ("lucide:youtube" or "youtube") as a whole SVG that takes the
// colour of the text around it, for the editor's icon picker. It is empty for an unknown name.
func LucideSVG(name string) string {
	name = strings.TrimPrefix(name, lucidePrefix)
	if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return ""
	}
	body, err := lucide.ReadFile("lucide/" + name + ".svg")
	if err != nil {
		return ""
	}
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor"` +
		` stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + string(body) + `</svg>`
}
