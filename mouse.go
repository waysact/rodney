package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type mouseSender func(proto.InputDispatchMouseEvent) error

func dispatchMouse(page *rod.Page, event proto.InputDispatchMouseEvent) error {
	return event.Call(page)
}

// A release must remain possible after the action's page deadline expires.
func mouseActionSender(page *rod.Page) mouseSender {
	return func(event proto.InputDispatchMouseEvent) error {
		if event.Type != proto.InputDispatchMouseEventTypeMouseReleased {
			return dispatchMouse(page, event)
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(page.GetContext()), 5*time.Second)
		defer cancel()
		return dispatchMouse(page.Context(ctx), event)
	}
}

func cmdMouse(args []string) {
	if len(args) == 0 {
		fatal("usage: rodney mouse move|click")
	}
	switch args[0] {
	case "move":
		cmdMouseMove(args[1:])
	case "click":
		cmdMouseClick(args[1:])
	default:
		fatal("usage: rodney mouse move|click")
	}
}

func cmdMouseMove(args []string) {
	opts, err := parseMouseMoveArgs(args)
	if err != nil {
		fatal("%v", err)
	}
	unlock, err := lockPointer()
	if err != nil {
		fatal("%v", err)
	}
	defer unlock()
	state, _, page := withPage()
	vp, err := pageViewport(page)
	if err != nil {
		fatal("%v", err)
	}
	store, err := readPointerStore()
	if err != nil {
		fatal("%v", err)
	}
	_, stateErr := currentPointer(store, state, page, vp)
	if errors.Is(stateErr, errPointerUncertain) {
		fatal("%v", stateErr)
	}
	start, stateErr := currentPointer(store, state, page, vp)
	if (opts.Duration != 0 || opts.Button != proto.InputMouseButtonNone) && stateErr != nil {
		fatal("%v", stateErr)
	}
	x, y, err := resolveMouseTarget(page, opts.Target, opts)
	if err != nil {
		fatal("%v", err)
	}
	page = timedPage(page, opts.Duration)
	status := "moving"
	if opts.Button != proto.InputMouseButtonNone {
		status = "in_progress"
	}
	snapshot := pointerSnapshot{X: start.X, Y: start.Y, Status: status}
	if err := savePointer(store, state, page, vp, snapshot); err != nil {
		fatal("%v", err)
	}
	if opts.Duration == 0 {
		zero := 0
		if err := dispatchMouse(page, proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMouseMoved, X: x, Y: y, Button: proto.InputMouseButtonNone, Buttons: &zero}); err != nil {
			fatal("mouse move failed: %v", err)
		}
		snapshot.X, snapshot.Y = x, y
	} else {
		send := mouseActionSender(page)
		motion := func(emit mouseSender) error {
			return runMouseMotion(emit, point{start.X, start.Y}, point{x, y}, vp, opts.Duration, opts.Button, func(p point) error { snapshot.X, snapshot.Y = p.X, p.Y; return nil })
		}
		if opts.Button == proto.InputMouseButtonNone {
			if err := motion(send); err != nil {
				fatal("mouse move failed: %v", err)
			}
		} else {
			released, err := runButtonAction(send, point{start.X, start.Y}, opts.Button, motion)
			if !released {
				fatal("mouse button state is uncertain: %v", err)
			}
			if saveErr := savePointer(store, state, page, vp, pointerSnapshot{X: snapshot.X, Y: snapshot.Y, Status: "ready"}); saveErr != nil {
				fatal("%v", saveErr)
			}
			if err != nil {
				fatal("mouse drag failed: %v", err)
			}
			fmt.Println("Mouse moved")
			return
		}
	}
	snapshot.Status = "ready"
	if err := savePointer(store, state, page, vp, snapshot); err != nil {
		fatal("%v", err)
	}
	fmt.Println("Mouse moved")
}

func runButtonAction(send mouseSender, start point, button proto.InputMouseButton, motion func(mouseSender) error) (bool, error) {
	mask := mouseButtonMask(button)
	last := start
	press := proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMousePressed, X: start.X, Y: start.Y, Button: button, Buttons: &mask, ClickCount: 1}
	pressErr := send(press)
	var motionErr error
	if pressErr == nil {
		motionErr = motion(func(event proto.InputDispatchMouseEvent) error {
			if err := send(event); err != nil {
				return err
			}
			if event.Type == proto.InputDispatchMouseEventTypeMouseMoved {
				last = point{event.X, event.Y}
			}
			return nil
		})
	}
	zero := 0
	release := proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMouseReleased, X: last.X, Y: last.Y, Button: button, Buttons: &zero, ClickCount: 1}
	releaseErr := send(release)
	return releaseErr == nil, errors.Join(pressErr, motionErr, releaseErr)
}

func cmdMouseClick(args []string) {
	fs := flag.NewFlagSet("mouse click", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	buttonText := fs.String("button", "left", "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fatal("usage: rodney mouse click [--button left|middle|right]")
	}
	button, err := parseMouseButton(*buttonText)
	if err != nil {
		fatal("%v", err)
	}
	unlock, err := lockPointer()
	if err != nil {
		fatal("%v", err)
	}
	defer unlock()
	state, _, page := withPage()
	vp, err := pageViewport(page)
	if err != nil {
		fatal("%v", err)
	}
	store, err := readPointerStore()
	if err != nil {
		fatal("%v", err)
	}
	snapshot, err := currentPointer(store, state, page, vp)
	if err != nil {
		fatal("%v", err)
	}
	snapshot.Status = "in_progress"
	if err := savePointer(store, state, page, vp, snapshot); err != nil {
		fatal("%v", err)
	}
	send := mouseActionSender(page)
	released, actionErr := runButtonAction(send, point{snapshot.X, snapshot.Y}, button, func(mouseSender) error { return nil })
	if !released {
		fatal("mouse button state is uncertain: %v", actionErr)
	}
	snapshot.Status = "ready"
	if err := savePointer(store, state, page, vp, snapshot); err != nil {
		fatal("%v", err)
	}
	if actionErr != nil {
		fatal("mouse click failed: %v", actionErr)
	}
	fmt.Println("Clicked")
}
