package main

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestPointerStateRejectsChangedViewport(t *testing.T) {
	stored := pointerSnapshot{X: 100, Y: 100, Viewport: Viewport{W: 800, H: 600}, Status: "ready"}
	if err := validatePointer(stored, Viewport{W: 900, H: 600}); err == nil {
		t.Fatal("accepted stale coordinate")
	}
	for _, status := range []string{"moving", "in_progress"} {
		stored.Status = status
		if err := validatePointer(stored, stored.Viewport); err == nil {
			t.Fatalf("accepted %s coordinate", status)
		}
	}
}

func TestPointerStateRejectsMalformedFile(t *testing.T) {
	t.Setenv("RODNEY_HOME", t.TempDir())
	if err := os.WriteFile(pointerPath(), []byte("{not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPointerStore(); err == nil {
		t.Fatal("accepted malformed pointer state")
	}
}

func TestPointerStateSessionAndTarget(t *testing.T) {
	t.Setenv("RODNEY_HOME", t.TempDir())
	page := env.browser.MustPage("about:blank")
	defer page.MustClose()
	state := &State{DebugURL: "browser-one"}
	vp := Viewport{W: 800, H: 600}
	store, err := readPointerStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := currentPointer(store, state, page, vp); err == nil {
		t.Fatal("accepted absent coordinate")
	}
	want := pointerSnapshot{X: 100, Y: 200, Status: "ready"}
	if err := savePointer(store, state, page, vp, want); err != nil {
		t.Fatal(err)
	}
	store, err = readPointerStore()
	if err != nil {
		t.Fatal(err)
	}
	got, err := currentPointer(store, state, page, vp)
	if err != nil || got.X != want.X || got.Y != want.Y {
		t.Fatalf("snapshot=%+v err=%v", got, err)
	}
	if _, err := currentPointer(store, &State{DebugURL: "browser-two"}, page, vp); err == nil {
		t.Fatal("accepted another browser")
	}
	other := env.browser.MustPage("about:blank")
	defer other.MustClose()
	if _, err := currentPointer(store, state, other, vp); err == nil {
		t.Fatal("accepted another target")
	}
	if _, err := currentPointer(store, state, page, Viewport{W: 900, H: 600}); err == nil {
		t.Fatal("accepted resized viewport")
	}
	want.Status = "moving"
	if err := savePointer(store, state, page, vp, want); err != nil {
		t.Fatal(err)
	}
	store, _ = readPointerStore()
	if _, err := currentPointer(store, state, page, vp); !errors.Is(err, errPointerMoving) {
		t.Fatalf("moving error=%v", err)
	}
	want.Status = "in_progress"
	if err := savePointer(store, state, page, vp, want); err != nil {
		t.Fatal(err)
	}
	store, _ = readPointerStore()
	if _, err := currentPointer(store, state, page, vp); !errors.Is(err, errPointerUncertain) {
		t.Fatalf("uncertain error=%v", err)
	}
}

func TestPointerStateUncertainAcrossTargets(t *testing.T) {
	t.Setenv("RODNEY_HOME", t.TempDir())
	first := env.browser.MustPage("about:blank")
	defer first.MustClose()
	second := env.browser.MustPage("about:blank")
	defer second.MustClose()
	state := &State{DebugURL: "same-browser"}
	vp := Viewport{W: 800, H: 600}
	store, err := readPointerStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := savePointer(store, state, first, vp, pointerSnapshot{X: 100, Y: 100, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	store, _ = readPointerStore()
	if err := savePointer(store, state, second, vp, pointerSnapshot{X: 200, Y: 200, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	store, _ = readPointerStore()
	if err := savePointer(store, state, first, vp, pointerSnapshot{X: 100, Y: 100, Status: "in_progress"}); err != nil {
		t.Fatal(err)
	}
	store, _ = readPointerStore()
	if _, err := currentPointer(store, state, second, vp); !errors.Is(err, errPointerUncertain) {
		t.Fatalf("second target error = %v", err)
	}
	if _, err := currentPointer(store, &State{DebugURL: "new-browser"}, second, vp); !errors.Is(err, errPointerMissing) {
		t.Fatalf("new browser error = %v", err)
	}
}

func TestPointerStateLock(t *testing.T) {
	t.Setenv("RODNEY_HOME", t.TempDir())
	unlock, err := lockPointer()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		second, err := lockPointer()
		if err == nil {
			second()
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("second lock did not block")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second lock did not resume")
	}
}

// Run with RODNEY_LONG_TEST=1 to verify that queued commands outlast a long move.
func TestPointerStateLockWaitsBeyondOldTimeout(t *testing.T) {
	if os.Getenv("RODNEY_LONG_TEST") != "1" {
		t.Skip("set RODNEY_LONG_TEST=1 to run the long lock test")
	}
	t.Setenv("RODNEY_HOME", t.TempDir())
	unlock, err := lockPointer()
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		second, err := lockPointer()
		if err == nil {
			second()
		}
		result <- err
	}()
	time.Sleep(36 * time.Second)
	unlock()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("second lock failed after first lock released: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second lock did not resume")
	}
}
