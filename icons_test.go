package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pngURL(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{200, 30, 30, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestUploadIcon(t *testing.T) {
	e := newEditor(t)

	// A wide picture is cropped square from the middle; a name that tries a path is made safe.
	got, err := e.UploadIcon("../../My Logo.JPG", pngURL(t, 400, 100))
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-logo.png" {
		t.Fatalf("file name %q", got)
	}
	f, err := os.Open(filepath.Join(e.core.Dir, "icons", got))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil || cfg.Width != iconSide || cfg.Height != iconSide {
		t.Fatalf("stored %dx%d, %v", cfg.Width, cfg.Height, err)
	}

	// The same name again does not replace the first.
	again, err := e.UploadIcon("my-logo.png", pngURL(t, 10, 10))
	if err != nil || again != "my-logo-2.png" {
		t.Fatalf("second upload: %q, %v", again, err)
	}
	icons, err := e.UserIcons()
	if err != nil || len(icons) != 2 || !strings.HasPrefix(icons[got], "data:image/png;base64,") {
		t.Fatalf("UserIcons: %d, %v", len(icons), err)
	}

	for name, url := range map[string]string{
		"not a data url": "hello",
		"not an image":   "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("hi")),
		"damaged base64": "data:image/png;base64,@@@",
	} {
		if _, err := e.UploadIcon("x", url); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLucidePicker(t *testing.T) {
	e := newEditor(t)
	names := e.LucideNames()
	if len(names) < 1000 || !strings.HasPrefix(names[0], "lucide:") {
		t.Fatalf("%d names, first %q", len(names), names[:1])
	}
	svgs := e.LucideSVGs([]string{"lucide:folder", "../etc/passwd", "nope"})
	if len(svgs) != 1 || !strings.Contains(svgs["lucide:folder"], "currentColor") {
		t.Fatalf("svgs: %v", svgs)
	}
}

func TestTestAction(t *testing.T) {
	e := newEditor(t) // dry run: nothing is opened
	if err := e.TestAction(`{"type":"open.url","url":"https://example.com"}`); err != nil {
		t.Fatal(err)
	}
	for name, a := range map[string]string{
		"no scheme":     `{"type":"open.url","url":"example.com"}`,
		"unknown":       `{"type":"nope"}`,
		"not an action": `[1]`,
		"toggle alone":  `{"type":"toggle","on":{"type":"delay","ms":1},"off":{"type":"delay","ms":1}}`,
	} {
		if err := e.TestAction(a); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
