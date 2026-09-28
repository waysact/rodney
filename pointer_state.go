package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/go-rod/rod"
	"github.com/gofrs/flock"
)

var (
	errPointerMissing   = errors.New("pointer position is not established; use mouse move --duration 0")
	errPointerMoving    = errors.New("pointer motion was interrupted; use mouse move --duration 0")
	errPointerUncertain = errors.New("pointer button state is uncertain; stop and restart Chromium")
)

type pointerSnapshot struct {
	X        float64  `json:"x"`
	Y        float64  `json:"y"`
	Viewport Viewport `json:"viewport"`
	Status   string   `json:"status"`
}

type pointerStore struct {
	BrowserURL string                     `json:"browser_url"`
	Entries    map[string]pointerSnapshot `json:"entries"`
}

func pointerPath() string     { return filepath.Join(stateDir(), "pointer.json") }
func pointerLockPath() string { return filepath.Join(stateDir(), "pointer.lock") }

func lockPointer() (func(), error) {
	if err := os.MkdirAll(stateDir(), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(pointerLockPath())
	if err := lock.Lock(); err != nil {
		return nil, fmt.Errorf("lock pointer state: %w", err)
	}
	return func() { _ = lock.Unlock() }, nil
}

func readPointerStore() (pointerStore, error) {
	data, err := os.ReadFile(pointerPath())
	if os.IsNotExist(err) {
		return pointerStore{Entries: map[string]pointerSnapshot{}}, nil
	}
	if err != nil {
		return pointerStore{}, err
	}
	var store pointerStore
	if err := json.Unmarshal(data, &store); err != nil {
		return pointerStore{}, fmt.Errorf("invalid pointer state: %w", err)
	}
	if store.Entries == nil {
		return pointerStore{}, errors.New("invalid pointer state: missing entries")
	}
	return store, nil
}

func writePointerStore(store pointerStore) error {
	if err := os.MkdirAll(stateDir(), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(store)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(stateDir(), ".pointer-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), pointerPath())
}

func validatePointer(snapshot pointerSnapshot, vp Viewport) error {
	if snapshot.Status == "in_progress" {
		return errPointerUncertain
	}
	if snapshot.Status == "moving" {
		return errPointerMoving
	}
	if snapshot.Status != "ready" {
		return errPointerMissing
	}
	if snapshot.Viewport != vp || vp.W <= 0 || vp.H <= 0 {
		return errPointerMissing
	}
	if math.IsNaN(snapshot.X) || math.IsNaN(snapshot.Y) || math.IsInf(snapshot.X, 0) || math.IsInf(snapshot.Y, 0) ||
		snapshot.X < 0 || snapshot.Y < 0 || snapshot.X >= float64(vp.W) || snapshot.Y >= float64(vp.H) {
		return errPointerMissing
	}
	return nil
}

func currentPointer(store pointerStore, state *State, page *rod.Page, vp Viewport) (pointerSnapshot, error) {
	if store.BrowserURL != state.DebugURL {
		return pointerSnapshot{}, errPointerMissing
	}
	for _, entry := range store.Entries {
		if entry.Status == "in_progress" {
			return pointerSnapshot{}, errPointerUncertain
		}
	}
	snapshot, ok := store.Entries[string(page.TargetID)]
	if !ok {
		return pointerSnapshot{}, errPointerMissing
	}
	if err := validatePointer(snapshot, vp); err != nil {
		return pointerSnapshot{}, err
	}
	return snapshot, nil
}

func savePointer(store pointerStore, state *State, page *rod.Page, vp Viewport, snapshot pointerSnapshot) error {
	if store.BrowserURL != state.DebugURL || store.Entries == nil {
		store = pointerStore{BrowserURL: state.DebugURL, Entries: map[string]pointerSnapshot{}}
	}
	snapshot.Viewport = vp
	store.Entries[string(page.TargetID)] = snapshot
	return writePointerStore(store)
}
