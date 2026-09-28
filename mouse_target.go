package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type mouseMoveOptions struct {
	Target           string
	Duration         time.Duration
	Button           proto.InputMouseButton
	AnchorX, AnchorY float64
	AnchorSet        bool
}

func parseAnchor(s string) (float64, float64, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0, errors.New("anchor must be X%,Y%")
	}
	values := [2]float64{}
	for i, part := range parts {
		if !strings.HasSuffix(part, "%") {
			return 0, 0, errors.New("anchor must be X%,Y%")
		}
		n, err := strconv.ParseFloat(strings.TrimSuffix(part, "%"), 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
			return 0, 0, errors.New("anchor percent must be 0 to 100")
		}
		values[i] = n / 100
	}
	return values[0], values[1], nil
}

func parseMouseMoveArgs(args []string) (mouseMoveOptions, error) {
	opts := mouseMoveOptions{Duration: 500 * time.Millisecond, Button: proto.InputMouseButtonNone, AnchorX: .5, AnchorY: .5}
	fs := flag.NewFlagSet("mouse move", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	durationText := fs.String("duration", "500", "")
	anchorText := fs.String("anchor", "", "")
	buttonText := fs.String("button", "", "")
	positional, err := parseInterleaved(fs, args)
	if err != nil || len(positional) != 1 {
		return opts, errors.New("usage: rodney mouse move [--anchor X%,Y%] [--duration ms] [--button left|middle|right] <target>")
	}
	opts.Target = positional[0]
	ms, err := strconv.ParseInt(*durationText, 10, 64)
	if err != nil || ms < 0 || ms > math.MaxInt64/int64(time.Millisecond) {
		return opts, errors.New("duration must be a nonnegative integer in milliseconds")
	}
	opts.Duration = time.Duration(ms) * time.Millisecond
	if *anchorText != "" {
		opts.AnchorX, opts.AnchorY, err = parseAnchor(*anchorText)
		if err != nil {
			return opts, err
		}
		opts.AnchorSet = true
	}
	if *buttonText != "" {
		button, err := parseMouseButton(*buttonText)
		if err != nil {
			return opts, err
		}
		opts.Button = button
		if opts.Duration == 0 {
			return opts, errors.New("button movement requires positive duration")
		}
	}
	_, _, coordinate, err := parseCoordinateTarget(opts.Target)
	if err != nil {
		return opts, err
	}
	if coordinate && opts.AnchorSet {
		return opts, errors.New("anchor cannot be used with a coordinate")
	}
	return opts, nil
}

func parseMouseButton(value string) (proto.InputMouseButton, error) {
	switch value {
	case "left":
		return proto.InputMouseButtonLeft, nil
	case "middle":
		return proto.InputMouseButtonMiddle, nil
	case "right":
		return proto.InputMouseButtonRight, nil
	default:
		return proto.InputMouseButtonNone, errors.New("button must be left, middle, or right")
	}
}

func parseCoordinateTarget(target string) (float64, float64, bool, error) {
	if len(target) == 0 {
		return 0, 0, false, errors.New("target is empty")
	}
	if !strings.Contains(target, ",") {
		return 0, 0, false, nil
	}
	first := target[0]
	firstPart, _, _ := strings.Cut(target, ",")
	nonfinite := strings.EqualFold(firstPart, "NaN") || strings.EqualFold(firstPart, "Inf") || strings.EqualFold(firstPart, "Infinity")
	if !(nonfinite || first >= '0' && first <= '9' || first == '-' || first == '+' || first == '.' && len(target) > 1 && target[1] >= '0' && target[1] <= '9') {
		return 0, 0, false, nil
	}
	parts := strings.Split(target, ",")
	if len(parts) != 2 {
		return 0, 0, true, errors.New("coordinate must be x,y")
	}
	x, ex := strconv.ParseFloat(parts[0], 64)
	y, ey := strconv.ParseFloat(parts[1], 64)
	if ex != nil || ey != nil || math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
		return 0, 0, true, errors.New("coordinate must contain finite x,y values")
	}
	return x, y, true, nil
}

func resolveMouseTarget(page *rod.Page, target string, opts mouseMoveOptions) (float64, float64, error) {
	vp, err := pageViewport(page)
	if err != nil {
		return 0, 0, err
	}
	x, y, coordinate, err := parseCoordinateTarget(target)
	if err != nil {
		return 0, 0, err
	}
	if !coordinate && target == "viewport" {
		x, y = math.Min(opts.AnchorX*float64(vp.W), float64(vp.W)-.5), math.Min(opts.AnchorY*float64(vp.H), float64(vp.H)-.5)
	} else if !coordinate {
		res, evalErr := page.Eval(`(selector, ax, ay) => {
		 const el = document.querySelector(selector);
		 if (!el) return {error:'target selector not found'};
		 const r = el.getBoundingClientRect();
		 if (r.width <= 0 || r.height <= 0) return {error:'target has no area'};
		 const x = Math.max(r.left+.5, Math.min(r.left+r.width*ax,r.right-.5));
		 const y = Math.max(r.top+.5, Math.min(r.top+r.height*ay,r.bottom-.5));
		 const hit = document.elementFromPoint(x,y);
		 if (x < 0 || y < 0 || x >= innerWidth || y >= innerHeight) return {error:'target point is outside the viewport'};
		 if (!hit || !el.contains(hit)) return {error:'target point is covered'};
		 return {x,y};
		}`, target, opts.AnchorX, opts.AnchorY)
		if evalErr != nil {
			return 0, 0, errors.New("cannot resolve mouse target")
		}
		if value := res.Value.Get("error"); !value.Nil() {
			return 0, 0, errors.New(value.Str())
		}
		x, y = res.Value.Get("x").Num(), res.Value.Get("y").Num()
	}
	if x < 0 || y < 0 || x >= float64(vp.W) || y >= float64(vp.H) {
		return 0, 0, errors.New("target point is outside the viewport")
	}
	if coordinate || target == "viewport" {
		res, err := page.Eval(`(x,y) => !!document.elementFromPoint(x,y)`, x, y)
		if err != nil {
			return 0, 0, fmt.Errorf("cannot check target hit: %w", err)
		}
		if !res.Value.Bool() {
			return 0, 0, errors.New("target point has no hit element")
		}
	}
	return x, y, nil
}
