package main

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"image/png"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

//go:embed frame.html
var frameHTML string

// renderFrameJS fills in the template and resolves with the frame's box in
// document coordinates. It uses textContent, so the title and URL need no
// escaping.
const renderFrameJS = `(src, title, url, width) => new Promise((resolve, reject) => {
	document.getElementById("tab").textContent = title || url;
	document.getElementById("url").textContent = url;
	const frame = document.getElementById("frame");
	frame.style.width = width + "px";
	const img = document.getElementById("page");
	img.onload = () => {
		const r = frame.getBoundingClientRect();
		resolve([r.left + scrollX, r.top + scrollY, r.width, r.height]);
	};
	img.onerror = () => reject(new Error("capture image did not load"));
	img.src = src;
})`

// renderFrame draws a plain browser-window mock-up around a capture: one
// tab with the page title, an address bar with the page URL, and three
// window dots. It is not Chrome's real UI. scale is the capture's image
// pixels per CSS pixel, and the framed image uses the same scale.
func renderFrame(browser *rod.Browser, shot []byte, title, pageURL string, scale float64) ([]byte, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(shot))
	if err != nil {
		return nil, fmt.Errorf("failed to read capture: %w", err)
	}
	fp, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank", Background: true})
	if err != nil {
		return nil, fmt.Errorf("failed to open frame page: %w", err)
	}
	defer fp.Close()
	if err := fp.SetDocumentContent(frameHTML); err != nil {
		return nil, fmt.Errorf("failed to load frame template: %w", err)
	}
	src := "data:image/png;base64," + base64.StdEncoding.EncodeToString(shot)
	res, err := fp.Eval(renderFrameJS, src, title, pageURL, float64(cfg.Width)/scale)
	if err != nil {
		return nil, fmt.Errorf("failed to render frame: %w", err)
	}
	a := res.Value.Arr()
	box := &proto.PageViewport{X: a[0].Num(), Y: a[1].Num(), Width: a[2].Num(), Height: a[3].Num()}
	return capture(fp, captureOptions{
		Region: func(*rod.Page) (*proto.PageViewport, error) { return box, nil },
		Scale:  scale,
	})
}
