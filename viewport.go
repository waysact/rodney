package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// Viewport is a viewport size in CSS pixels.
type Viewport struct {
	W int `json:"w"`
	H int `json:"h"`
}

func (v Viewport) String() string { return fmt.Sprintf("%dx%d", v.W, v.H) }

var defaultViewport = Viewport{W: 1280, H: 800}

func parseViewport(s string) (Viewport, error) {
	bad := fmt.Errorf("invalid viewport %q (use WxH, for example 1280x800)", s)
	ws, hs, ok := strings.Cut(strings.ToLower(s), "x")
	if !ok {
		return Viewport{}, bad
	}
	w, errW := strconv.Atoi(ws)
	h, errH := strconv.Atoi(hs)
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return Viewport{}, bad
	}
	return Viewport{W: w, H: h}, nil
}

// pageViewport returns the page's layout viewport as the page sees it.
func pageViewport(page *rod.Page) (Viewport, error) {
	res, err := page.Eval(`() => [innerWidth, innerHeight]`)
	if err != nil {
		return Viewport{}, fmt.Errorf("failed to read viewport: %w", err)
	}
	a := res.Value.Arr()
	return Viewport{W: a[0].Int(), H: a[1].Int()}, nil
}

// waitForViewport polls until the viewport equals want or 500 ms pass, and
// returns the last measurement. A window resize reaches the page
// asynchronously.
func waitForViewport(page *rod.Page, want Viewport) (Viewport, error) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		got, err := pageViewport(page)
		if err != nil || got == want || time.Now().After(deadline) {
			return got, err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// applyViewport sets the viewport through the browser window, which stays
// set after this process exits. A device metrics override would not: its
// lifetime depends on the Chrome version. The window is larger than the
// viewport by the browser UI, so the size is corrected by the measured
// difference.
func applyViewport(page *rod.Page, want Viewport) error {
	if err := (proto.EmulationClearDeviceMetricsOverride{}).Call(page); err != nil {
		return fmt.Errorf("failed to clear viewport override: %w", err)
	}
	got, err := pageViewport(page)
	if err != nil {
		return err
	}
	if got == want {
		return nil
	}
	bounds, err := page.GetWindow()
	if err != nil {
		return fmt.Errorf("failed to read window bounds: %w", err)
	}
	if bounds.Width == nil || bounds.Height == nil {
		return fmt.Errorf("browser reported no window size")
	}
	winW := *bounds.Width + want.W - got.W
	winH := *bounds.Height + want.H - got.H
	for attempt := 0; attempt < 2; attempt++ {
		err := page.SetWindow(&proto.BrowserBounds{Width: &winW, Height: &winH, WindowState: proto.BrowserWindowStateNormal})
		if err != nil {
			return fmt.Errorf("failed to set window bounds: %w", err)
		}
		if got, err = waitForViewport(page, want); err != nil {
			return err
		}
		if got == want {
			return nil
		}
		winW += want.W - got.W
		winH += want.H - got.H
	}
	return fmt.Errorf("viewport is %s after resizing the window, wanted %s", got, want)
}

// ensureViewport applies the session's viewport to a page. A session made
// with "rodney connect" has no viewport, and the page keeps its window.
func ensureViewport(page *rod.Page, s *State) error {
	if s.Viewport == nil {
		return nil
	}
	return applyViewport(page, *s.Viewport)
}

func cmdViewport(args []string) {
	if len(args) > 1 {
		fatal("usage: rodney viewport [WxH]")
	}
	s, _, page := withPage()
	if len(args) == 0 {
		vp, err := pageViewport(page)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(vp)
		return
	}
	vp, err := parseViewport(args[0])
	if err != nil {
		fatal("%v", err)
	}
	if err := applyViewport(page, vp); err != nil {
		fatal("%v", err)
	}
	s.Viewport = &vp
	if err := saveState(s); err != nil {
		fatal("failed to save state: %v", err)
	}
	fmt.Println(vp)
}
