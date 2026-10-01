package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // uploads may be JPEGs
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	xdraw "golang.org/x/image/draw"

	"github.com/asmsaifs/techo5-streamdeck/internal/render"
	"github.com/asmsaifs/techo5-streamdeck/internal/store"
)

// iconSide is the size an uploaded icon is stored at: bigger than any cell, so it stays sharp on
// a 1280 by 800 Show, and small enough to send to the editor as a data URL.
const iconSide = 256

// maxUpload is the biggest picture the editor may hand over, as a data URL's decoded bytes.
const maxUpload = 10 << 20

// LucideNames lists the bundled line icons ("lucide:name").
func (e *Editor) LucideNames() []string { return render.LucideNames() }

// LucideSVGs draws the named bundled icons as SVG text, by name, for the picker's visible page.
func (e *Editor) LucideSVGs(names []string) map[string]string {
	out := make(map[string]string, len(names))
	for _, n := range names {
		if s := render.LucideSVG(n); s != "" {
			out[n] = s
		}
	}
	return out
}

// UserIcons lists the pictures in icons/, by file name, each as a PNG data URL.
func (e *Editor) UserIcons() (map[string]string, error) {
	dir := filepath.Join(e.core.Dir, store.IconsDir)
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, ent := range ents {
		if ent.IsDir() || !strings.EqualFold(filepath.Ext(ent.Name()), ".png") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err == nil {
			out[ent.Name()] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
		}
	}
	return out, nil
}

var unsafeName = regexp.MustCompile(`[^a-z0-9_-]+`)

// UploadIcon stores a picture the user chose as a square PNG in icons/, cropped from the middle,
// and returns the file name to put in a button's icon. dataURL is what a browser's FileReader
// gives; name is only a hint for the file name. A name already taken gets a number.
func (e *Editor) UploadIcon(name, dataURL string) (string, error) {
	_, b64, ok := strings.Cut(dataURL, ";base64,")
	if !ok {
		return "", errors.New("the picture did not arrive as a data URL")
	}
	if len(b64) > maxUpload*4/3+4 {
		return "", errors.New("that picture is bigger than 10 MB")
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("the picture is damaged: %w", err)
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("only PNG and JPEG pictures can be icons")
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2))
	dst := image.NewRGBA(image.Rect(0, 0, iconSide, iconSide))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return "", err
	}

	stem := strings.TrimSuffix(strings.ToLower(filepath.Base(name)), strings.ToLower(filepath.Ext(name)))
	stem = strings.Trim(unsafeName.ReplaceAllString(stem, "-"), "-")
	if stem == "" {
		stem = "icon"
	}
	dir := filepath.Join(e.core.Dir, store.IconsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for n := 1; ; n++ {
		file := stem + ".png"
		if n > 1 {
			file = fmt.Sprintf("%s-%d.png", stem, n)
		}
		// O_EXCL: never replace an icon that a button may already use.
		f, err := os.OpenFile(filepath.Join(dir, file), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(out.Bytes())
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return file, werr
	}
}
