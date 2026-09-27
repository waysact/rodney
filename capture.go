package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// captureOptions controls one capture. Sizes and regions are in CSS pixels.
type captureOptions struct {
	// Width and Height set a temporary viewport for this capture only.
	// Zero keeps the current size.
	Width, Height int
	// FullPage captures the whole document instead of the viewport.
	FullPage bool
	// Region returns the region to capture, in document coordinates. It is
	// called after the temporary viewport is in place. It takes precedence
	// over FullPage.
	Region func(page *rod.Page) (*proto.PageViewport, error)
	// Scale is the number of image pixels per CSS pixel. Zero means the
	// page's devicePixelRatio.
	Scale float64
	// Warn receives a warning when Region extends beyond the viewport.
	Warn io.Writer
}

type pageMetrics struct {
	W, H             int
	DPR              float64
	ScrollX, ScrollY float64
}

func readMetrics(page *rod.Page) (pageMetrics, error) {
	res, err := page.Eval(`() => [innerWidth, innerHeight, devicePixelRatio, scrollX, scrollY]`)
	if err != nil {
		return pageMetrics{}, fmt.Errorf("failed to read page metrics: %w", err)
	}
	a := res.Value.Arr()
	return pageMetrics{W: a[0].Int(), H: a[1].Int(), DPR: a[2].Num(), ScrollX: a[3].Num(), ScrollY: a[4].Num()}, nil
}

// waitFrames waits for two animation frames, so that code that reacts to
// resize or scroll in the next frame, such as driver.js, has run. A
// background tab gets no animation frames, so a timer ends the wait after
// 500 ms.
func waitFrames(page *rod.Page) error {
	_, err := page.Eval(`() => new Promise(resolve => {
		let done = false;
		const finish = () => { if (!done) { done = true; resolve(); } };
		requestAnimationFrame(() => requestAnimationFrame(finish));
		setTimeout(finish, 500);
	})`)
	return err
}

// capture takes a PNG screenshot. It never leaves an override behind: Chrome
// does not reliably drop a device metrics override when this process
// detaches, so the override is cleared explicitly.
func capture(page *rod.Page, o captureOptions) (data []byte, err error) {
	m, err := readMetrics(page)
	if err != nil {
		return nil, err
	}
	if (o.Width != 0 && o.Width != m.W) || (o.Height != 0 && o.Height != m.H) {
		w, h := m.W, m.H
		if o.Width != 0 {
			w = o.Width
		}
		if o.Height != 0 {
			h = o.Height
		}
		// Install cleanup before setting the override: Chrome may receive
		// the command even if its response loses a race with the timeout.
		defer func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(page.GetContext()), 5*time.Second)
			defer cancel()
			if clearErr := (proto.EmulationClearDeviceMetricsOverride{}).Call(page.Context(ctx)); clearErr != nil {
				data = nil
				err = errors.Join(err, fmt.Errorf("failed to clear viewport override: %w", clearErr))
			}
		}()
		// DeviceScaleFactor 0 keeps the browser's device scale factor.
		if err := (proto.EmulationSetDeviceMetricsOverride{Width: w, Height: h}).Call(page); err != nil {
			return nil, fmt.Errorf("failed to set viewport: %w", err)
		}
		if err := waitFrames(page); err != nil {
			return nil, err
		}
		if m, err = readMetrics(page); err != nil {
			return nil, err
		}
	}

	var clip *proto.PageViewport
	beyond := false
	switch {
	case o.Region != nil:
		if clip, err = o.Region(page); err != nil {
			return nil, err
		}
		// The region function may scroll, so measure again.
		if m, err = readMetrics(page); err != nil {
			return nil, err
		}
		beyond = clip.X < m.ScrollX || clip.Y < m.ScrollY ||
			clip.X+clip.Width > m.ScrollX+float64(m.W) || clip.Y+clip.Height > m.ScrollY+float64(m.H)
		if beyond && o.Warn != nil {
			fmt.Fprintf(o.Warn, "warning: capture region %gx%g extends beyond the %dx%d viewport; position: fixed elements render at their viewport position (use a larger -w/-h)\n",
				clip.Width, clip.Height, m.W, m.H)
		}
	case o.FullPage:
		lm, err := proto.PageGetLayoutMetrics{}.Call(page)
		if err != nil {
			return nil, fmt.Errorf("failed to read layout metrics: %w", err)
		}
		clip = &proto.PageViewport{Width: lm.CSSContentSize.Width, Height: lm.CSSContentSize.Height}
		beyond = true
	default:
		clip = &proto.PageViewport{X: m.ScrollX, Y: m.ScrollY, Width: float64(m.W), Height: float64(m.H)}
	}

	scale := o.Scale
	if scale == 0 {
		scale = m.DPR
	}
	c := *clip
	// clip.scale multiplies the device scale factor.
	c.Scale = scale / m.DPR
	res, err := proto.PageCaptureScreenshot{
		Format:                proto.PageCaptureScreenshotFormatPng,
		Clip:                  &c,
		CaptureBeyondViewport: beyond,
	}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("screenshot failed: %w", err)
	}
	return res.Data, nil
}

// elementClipJS measures the target and the included elements, in document
// coordinates, and returns their union grown by margin and clamped to the
// document. The positional selector keeps its first-match semantics through
// page.Element; each include uses all matches.
const elementClipJS = `function (includes, margin) {
	const box = e => {
		const style = getComputedStyle(e);
		if (style.display === "none" || style.visibility === "hidden") return null;
		const r = e.getBoundingClientRect();
		return r.width > 0 && r.height > 0 ? [r.left + scrollX, r.top + scrollY, r.right + scrollX, r.bottom + scrollY] : null;
	};
	let u = box(this);
	if (!u) return {error: "target element has an empty box"};
	for (const sel of includes) {
		const boxes = [...document.querySelectorAll(sel)].map(box).filter(Boolean);
		if (boxes.length === 0) return {error: "--include " + sel + " matched no visible element"};
		for (const b of boxes) u = [Math.min(u[0], b[0]), Math.min(u[1], b[1]), Math.max(u[2], b[2]), Math.max(u[3], b[3])];
	}
	const doc = document.documentElement;
	const maxX = Math.max(doc.scrollWidth, innerWidth), maxY = Math.max(doc.scrollHeight, innerHeight);
	const x0 = Math.max(0, u[0] - margin), y0 = Math.max(0, u[1] - margin);
	const x1 = Math.min(maxX, u[2] + margin), y1 = Math.min(maxY, u[3] + margin);
	return {error: "", x: x0, y: y0, width: x1 - x0, height: y1 - y0};
}`

// elementClip returns the region that screenshot-el captures. It scrolls the
// target into view when it is not fully visible, and waits two frames so
// that overlays that listen to scroll can reposition before it measures.
func elementClip(page *rod.Page, selector string, includes []string, margin float64) (*proto.PageViewport, error) {
	if includes == nil {
		includes = []string{}
	}
	el, err := page.Element(selector)
	if err != nil {
		return nil, fmt.Errorf("element not found: %w", err)
	}
	if err := el.WaitVisible(); err != nil {
		return nil, fmt.Errorf("element not visible: %w", err)
	}
	scrolled, err := el.Eval(`() => {
		const r = this.getBoundingClientRect();
		const inside = r.top >= 0 && r.left >= 0 && r.bottom <= innerHeight && r.right <= innerWidth;
		if (!inside) this.scrollIntoView({behavior: "instant", block: "center", inline: "center"});
		return !inside;
	}`)
	if err != nil {
		return nil, err
	}
	if scrolled.Value.Bool() {
		if err := waitFrames(page); err != nil {
			return nil, err
		}
	}
	res, err := el.Eval(elementClipJS, includes, margin)
	if err != nil {
		return nil, err
	}
	v := res.Value
	if msg := v.Get("error").Str(); msg != "" {
		return nil, errors.New(msg)
	}
	return &proto.PageViewport{X: v.Get("x").Num(), Y: v.Get("y").Num(), Width: v.Get("width").Num(), Height: v.Get("height").Num()}, nil
}

// parseInterleaved parses flags that come before, between or after the
// positional arguments, and returns the positional arguments in order.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for len(args) > 0 {
		if args[0] == "--" {
			return append(positional, args[1:]...), nil
		}
		if args[0] == "-" || !strings.HasPrefix(args[0], "-") {
			positional = append(positional, args[0])
			args = args[1:]
			continue
		}
		// Parse one flag at a time so that FlagSet cannot consume the
		// terminator. A non-boolean flag may itself take "--" as a value.
		n := 1
		name := strings.TrimPrefix(strings.TrimPrefix(args[0], "-"), "-")
		if !strings.Contains(name, "=") && len(args) > 1 {
			if f := fs.Lookup(name); f != nil {
				b, ok := f.Value.(interface{ IsBoolFlag() bool })
				if !ok || !b.IsBoolFlag() {
					n = 2
				}
			}
		}
		if err := fs.Parse(args[:n]); err != nil {
			return nil, err
		}
		args = args[n:]
	}
	return positional, nil
}

// stringList is a flag that can be given more than once.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }
