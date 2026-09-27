package main

import (
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("capture is not a PNG: %v", err)
	}
	return img
}

func isRed(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r>>8 > 200 && g>>8 < 60 && b>>8 < 60
}

func assertSize(t *testing.T, img image.Image, w, h int) {
	t.Helper()
	if got := img.Bounds().Size(); got.X != w || got.Y != h {
		t.Fatalf("image is %dx%d, want %dx%d", got.X, got.Y, w, h)
	}
}

// capturePage opens a fixture with an 800x600 viewport.
func capturePage(t *testing.T, fixture string) *rod.Page {
	t.Helper()
	page := navigateTo(t, "/fixture/"+fixture)
	if err := applyViewport(page, Viewport{800, 600}); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestCapture_Viewport(t *testing.T) {
	page := capturePage(t, "tall")
	data, err := capture(page, captureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertSize(t, decodePNG(t, data), 800, 600)
}

func TestCapture_TemporaryOverrideIsCleared(t *testing.T) {
	page := capturePage(t, "tall")
	data, err := capture(page, captureOptions{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	assertSize(t, decodePNG(t, data), 640, 480)
	if got, _ := pageViewport(page); got != (Viewport{800, 600}) {
		t.Fatalf("viewport after capture = %v, want 800x600", got)
	}
}

func TestCapture_ReportsCaptureAndClearFailures(t *testing.T) {
	page := env.browser.MustPage(env.server.URL + "/fixture/tall").MustWaitLoad()
	if err := applyViewport(page, Viewport{800, 600}); err != nil {
		t.Fatal(err)
	}
	_, err := capture(page, captureOptions{Width: 640, Region: func(page *rod.Page) (*proto.PageViewport, error) {
		if err := page.Close(); err != nil {
			return nil, err
		}
		return &proto.PageViewport{Width: 640, Height: 600}, nil
	}})
	if err == nil || !strings.Contains(err.Error(), "failed to read page metrics") || !strings.Contains(err.Error(), "failed to clear viewport override") {
		t.Fatalf("capture error = %v, want capture and clear failures", err)
	}
}

func TestCapture_FullPageKeepsFixedElement(t *testing.T) {
	page := capturePage(t, "tall")
	data, err := capture(page, captureOptions{FullPage: true})
	if err != nil {
		t.Fatal(err)
	}
	img := decodePNG(t, data)
	if img.Bounds().Dy() < 3000 {
		t.Fatalf("full-page capture is %d px high, want at least 3000", img.Bounds().Dy())
	}
	if !isRed(img.At(35, 35)) {
		t.Error("fixed element is not at its viewport position (35,35)")
	}
	if isRed(img.At(35, 635)) {
		t.Error("fixed element also appears one viewport height lower")
	}
}

func TestCapture_WaitsForReposition(t *testing.T) {
	page := capturePage(t, "reposition")
	data, err := capture(page, captureOptions{Width: 500, Height: 400})
	if err != nil {
		t.Fatal(err)
	}
	img := decodePNG(t, data)
	if !isRed(img.At(480, 20)) || isRed(img.At(20, 20)) {
		t.Fatal("capture does not show the box at its position after the resize")
	}
}

func TestCapture_Scale(t *testing.T) {
	page := capturePage(t, "tall")
	data, err := capture(page, captureOptions{Scale: 2})
	if err != nil {
		t.Fatal(err)
	}
	assertSize(t, decodePNG(t, data), 1600, 1200)
}

func TestCapture_ScaleIsAbsoluteOnScale2Browser(t *testing.T) {
	b := launchBrowser(t, "2")
	page := b.MustPage(env.server.URL + "/fixture/tall").MustWaitLoad()
	// Chrome's window minimum can prevent a 400px viewport; the scale
	// behavior under test only needs these CSS dimensions.
	if err := (proto.EmulationSetDeviceMetricsOverride{Width: 400, Height: 300}).Call(page); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		scale float64
		w, h  int
	}{{0, 800, 600}, {1, 400, 300}, {2, 800, 600}, {3, 1200, 900}}
	for _, c := range cases {
		data, err := capture(page, captureOptions{Scale: c.scale})
		if err != nil {
			t.Fatal(err)
		}
		assertSize(t, decodePNG(t, data), c.w, c.h)
	}
}

func TestRenderFrame(t *testing.T) {
	page := capturePage(t, "tall")
	shot, err := capture(page, captureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := len(env.browser.MustPages())
	framed, err := renderFrame(env.browser, shot, "Tall", env.server.URL+"/fixture/tall", 1)
	if err != nil {
		t.Fatal(err)
	}
	if after := len(env.browser.MustPages()); after != before {
		t.Fatalf("page count changed from %d to %d", before, after)
	}
	img := decodePNG(t, framed)
	// The frame has a 1 px border on each side and the tab and address bars on top.
	if got := img.Bounds().Dx(); got != 802 {
		t.Errorf("framed width = %d, want 802", got)
	}
	if got := img.Bounds().Dy(); got < 660 {
		t.Errorf("framed height = %d, want at least 660", got)
	}
	// The capture's fixed red square sits under the bars, inside the border.
	bottom := img.Bounds().Dy() - 1 - 600
	if !isRed(img.At(1+35, bottom+35)) {
		t.Error("the capture is not drawn inside the frame")
	}
}

func assertClip(t *testing.T, got *proto.PageViewport, x, y, w, h float64) {
	t.Helper()
	if got.X != x || got.Y != y || got.Width != w || got.Height != h {
		t.Fatalf("clip = {%g %g %g %g}, want {%g %g %g %g}", got.X, got.Y, got.Width, got.Height, x, y, w, h)
	}
}

func TestElementClip(t *testing.T) {
	page := capturePage(t, "elements")
	cases := []struct {
		name     string
		selector string
		includes []string
		margin   float64
		x, y     float64
		w, h     float64
	}{
		{"target", "#target", nil, 0, 100, 100, 50, 40},
		{"margin", "#target", nil, 10, 90, 90, 70, 60},
		{"clamped", "#edge", nil, 20, 0, 0, 30, 30},
		{"include all matches", "#target", []string{".extra"}, 0, 100, 100, 220, 230},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clip, err := elementClip(page, c.selector, c.includes, c.margin)
			if err != nil {
				t.Fatal(err)
			}
			assertClip(t, clip, c.x, c.y, c.w, c.h)
		})
	}
}

func TestElementClip_IncludeWithoutVisibleMatch(t *testing.T) {
	page := capturePage(t, "elements")
	for _, selector := range []string{".hidden", ".invisible"} {
		t.Run(selector, func(t *testing.T) {
			_, err := elementClip(page, "#target", []string{selector}, 0)
			if err == nil || !strings.Contains(err.Error(), "--include "+selector) {
				t.Fatalf("err = %v, want an error that names --include %s", err, selector)
			}
		})
	}
}

func TestElementClip_ScrollsTargetIntoView(t *testing.T) {
	page := capturePage(t, "elements")
	clip, err := elementClip(page, "#far", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertClip(t, clip, 100, 1500, 50, 50)
	m, err := readMetrics(page)
	if err != nil {
		t.Fatal(err)
	}
	if m.ScrollY == 0 {
		t.Fatal("page did not scroll to the target")
	}
}

func TestCapture_RegionBeyondViewport(t *testing.T) {
	page := navigateTo(t, "/fixture/big")
	// Chrome's window minimum can prevent a 400px viewport.
	if err := (proto.EmulationSetDeviceMetricsOverride{Width: 400, Height: 300}).Call(page); err != nil {
		t.Fatal(err)
	}
	var warn strings.Builder
	data, err := capture(page, captureOptions{
		Warn: &warn,
		Region: func(p *rod.Page) (*proto.PageViewport, error) {
			return elementClip(p, "#big", nil, 0)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	img := decodePNG(t, data)
	assertSize(t, img, 600, 500)
	if !isRed(img.At(590, 490)) {
		t.Error("the part of the region outside the viewport is not rendered")
	}
	if !strings.Contains(warn.String(), "extends beyond") {
		t.Errorf("no warning; got %q", warn.String())
	}
}

func TestParseInterleaved(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var includes stringList
	fs.Var(&includes, "include", "")
	margin := fs.Float64("margin", 0, "")
	pos, err := parseInterleaved(fs, []string{"#a", "--include", ".x", "--margin", "8", "out.png", "--include", ".y"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pos, " ") != "#a out.png" || strings.Join(includes, " ") != ".x .y" || *margin != 8 {
		t.Fatalf("got positional %v, includes %v, margin %v", pos, includes, *margin)
	}
}

func TestParseInterleaved_Terminator(t *testing.T) {
	for _, args := range [][]string{
		{"--", "#a", "-output.png", "--include", ".x"},
		{"--margin", "8", "--", "#a", "-output.png", "--include", ".x"},
		{"#a", "--", "-output.png", "--include", ".x"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			var includes stringList
			fs.Var(&includes, "include", "")
			fs.Float64("margin", 0, "")
			pos, err := parseInterleaved(fs, args)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(pos, " ") != "#a -output.png --include .x" || len(includes) != 0 {
				t.Fatalf("got positional %v, includes %v", pos, includes)
			}
		})
	}
}

func TestParseInterleaved_TerminatorAsFlagValue(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	include := fs.String("include", "", "")
	margin := fs.Float64("margin", 0, "")
	pos, err := parseInterleaved(fs, []string{"--include", "--", "#a", "--margin=8", "out.png"})
	if err != nil || *include != "--" || *margin != 8 || strings.Join(pos, " ") != "#a out.png" {
		t.Fatalf("got positional %v, include %q, margin %g, error %v", pos, *include, *margin, err)
	}
}

func TestElementClip_SmoothScroll(t *testing.T) {
	page := capturePage(t, "elements")
	page.MustEval(`() => {
		document.documentElement.style.scrollBehavior = "smooth";
		const overlay = document.createElement("div");
		overlay.id = "overlay";
		overlay.style.cssText = "position:fixed;left:0;top:0;width:20px;height:20px";
		document.body.append(overlay);
		addEventListener("scroll", () => requestAnimationFrame(() => {
			overlay.style.top = document.querySelector("#far").getBoundingClientRect().top + "px";
		}));
	}`)
	clip, err := elementClip(page, "#far", []string{"#overlay"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertClip(t, clip, 0, 1500, 150, 50)
	if visible := page.MustEval(`() => {
		const r = document.querySelector("#far").getBoundingClientRect();
		return r.top >= 0 && r.bottom <= innerHeight;
	}`).Bool(); !visible {
		t.Fatal("target is still outside the viewport after scrolling")
	}
}
