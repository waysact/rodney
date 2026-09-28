package main

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

func TestMouseActionPressMoveRelease(t *testing.T) {
	for _, tc := range []struct {
		button proto.InputMouseButton
		mask   int
	}{{proto.InputMouseButtonLeft, 1}, {proto.InputMouseButtonMiddle, 4}, {proto.InputMouseButtonRight, 2}} {
		var events []proto.InputDispatchMouseEvent
		send := func(e proto.InputDispatchMouseEvent) error { events = append(events, e); return nil }
		end := point{160, 170}
		released, err := runButtonAction(send, point{100, 110}, tc.button, func(emit mouseSender) error {
			return emit(proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMouseMoved, X: end.X, Y: end.Y, Button: tc.button, Buttons: &tc.mask})
		})
		if err != nil || !released || len(events) != 3 {
			t.Fatalf("button %s events=%+v released=%v err=%v", tc.button, events, released, err)
		}
		if events[0].Type != proto.InputDispatchMouseEventTypeMousePressed || events[0].X != 100 || events[0].Y != 110 || events[0].Button != tc.button || *events[0].Buttons != tc.mask || events[0].ClickCount != 1 {
			t.Fatalf("press=%+v", events[0])
		}
		if events[1].Button != tc.button || *events[1].Buttons != tc.mask {
			t.Fatalf("move=%+v", events[1])
		}
		if events[2].Type != proto.InputDispatchMouseEventTypeMouseReleased || events[2].X != end.X || events[2].Y != end.Y || events[2].Button != tc.button || *events[2].Buttons != 0 || events[2].ClickCount != 1 {
			t.Fatalf("release=%+v", events[2])
		}
	}
}

func TestMouseActionReleasesAfterMoveError(t *testing.T) {
	var calls []proto.InputDispatchMouseEventType
	send := func(e proto.InputDispatchMouseEvent) error {
		calls = append(calls, e.Type)
		if e.Type == proto.InputDispatchMouseEventTypeMouseMoved {
			return errors.New("move")
		}
		return nil
	}
	one := 1
	released, err := runButtonAction(send, point{150, 150}, proto.InputMouseButtonLeft, func(emit mouseSender) error {
		return emit(proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMouseMoved, X: 160, Y: 150, Button: proto.InputMouseButtonLeft, Buttons: &one})
	})
	want := []proto.InputDispatchMouseEventType{proto.InputDispatchMouseEventTypeMousePressed, proto.InputDispatchMouseEventTypeMouseMoved, proto.InputDispatchMouseEventTypeMouseReleased}
	if !released || err == nil || !reflect.DeepEqual(calls, want) {
		t.Fatalf("released=%v calls=%v err=%v", released, calls, err)
	}
}

func TestMouseActionUncertainOnReleaseFailure(t *testing.T) {
	send := func(e proto.InputDispatchMouseEvent) error {
		if e.Type == proto.InputDispatchMouseEventTypeMouseReleased {
			return errors.New("release")
		}
		return nil
	}
	released, err := runButtonAction(send, point{150, 150}, proto.InputMouseButtonLeft, func(mouseSender) error { return nil })
	if released || err == nil {
		t.Fatalf("released=%v err=%v", released, err)
	}
}

func TestMouseActionReleasesAtLastConfirmedPoint(t *testing.T) {
	var releasedAt point
	moves := 0
	one := 1
	send := func(e proto.InputDispatchMouseEvent) error {
		if e.Type == proto.InputDispatchMouseEventTypeMouseMoved {
			moves++
			if moves == 2 {
				return errors.New("second move")
			}
		}
		if e.Type == proto.InputDispatchMouseEventTypeMouseReleased {
			releasedAt = point{e.X, e.Y}
		}
		return nil
	}
	released, err := runButtonAction(send, point{100, 100}, proto.InputMouseButtonLeft, func(emit mouseSender) error {
		for _, x := range []float64{150, 200} {
			if err := emit(proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMouseMoved, X: x, Y: 100, Button: proto.InputMouseButtonLeft, Buttons: &one}); err != nil {
				return err
			}
		}
		return nil
	})
	if !released || err == nil || releasedAt != (point{150, 100}) {
		t.Fatalf("released=%v point=%v err=%v", released, releasedAt, err)
	}
}

func TestMouseReleaseUsesFreshContext(t *testing.T) {
	page := env.browser.MustPage("about:blank")
	defer page.MustClose()
	expired := page.Timeout(time.Nanosecond)
	time.Sleep(time.Millisecond)
	release := proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMouseReleased, X: 10, Y: 10, Button: proto.InputMouseButtonLeft}
	if err := dispatchMouse(expired, release); err == nil {
		t.Fatal("expired page context unexpectedly sent a mouse event")
	}
	if err := mouseActionSender(expired)(release); err != nil {
		t.Fatalf("release through fresh context failed: %v", err)
	}
}
