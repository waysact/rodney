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
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
	"github.com/rivo/uniseg"
)

func parseKeyChord(chord string) ([]input.Key, input.Key, error) {
	parts := strings.Split(chord, "+")
	if len(parts) == 0 {
		return nil, 0, errors.New("invalid key chord")
	}
	modifiers := map[string]input.Key{
		"control": input.ControlLeft, "meta": input.MetaLeft,
		"alt": input.AltLeft, "shift": input.ShiftLeft,
	}
	named := map[string]input.Key{
		"enter": input.Enter, "tab": input.Tab, "escape": input.Escape,
		"arrowup": input.ArrowUp, "arrowdown": input.ArrowDown,
		"arrowleft": input.ArrowLeft, "arrowright": input.ArrowRight,
		"backspace": input.Backspace, "delete": input.Delete,
		"insert": input.Insert, "space": input.Space,
		"home": input.Home, "end": input.End,
		"pageup": input.PageUp, "pagedown": input.PageDown,
		"f1": input.F1, "f2": input.F2, "f3": input.F3, "f4": input.F4,
		"f5": input.F5, "f6": input.F6, "f7": input.F7, "f8": input.F8,
		"f9": input.F9, "f10": input.F10, "f11": input.F11, "f12": input.F12,
	}
	var mods []input.Key
	seen := map[string]bool{}
	for _, part := range parts[:len(parts)-1] {
		name := strings.ToLower(part)
		mod, ok := modifiers[name]
		if !ok || seen[name] {
			return nil, 0, errors.New("invalid key chord")
		}
		mods = append(mods, mod)
		seen[name] = true
	}
	base := strings.ToLower(parts[len(parts)-1])
	if key, ok := named[base]; ok {
		return mods, key, nil
	}
	if len(mods) == 0 || len(base) != 1 {
		return nil, 0, errors.New("invalid key chord")
	}
	c := base[0]
	if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
		return nil, 0, errors.New("invalid key chord")
	}
	key := input.Key(c)
	if seen["shift"] {
		shifted, ok := key.Shift()
		if !ok {
			return nil, 0, errors.New("invalid key chord")
		}
		key = shifted
	}
	return mods, key, nil
}

func sendKey(page *rod.Page, mods []input.Key, key input.Key) (err error) {
	pressed := make([]input.Key, 0, len(mods)+1)
	mask := 0
	defer func() {
		for i := len(pressed) - 1; i >= 0; i-- {
			pressedKey := pressed[i]
			if pressedKey.Modifier() != 0 {
				mask &^= pressedKey.Modifier()
			}
			if releaseErr := pressedKey.Encode(proto.InputDispatchKeyEventTypeKeyUp, mask).Call(page); releaseErr != nil {
				err = errors.Join(err, releaseErr)
			}
		}
	}()
	for _, mod := range mods {
		mask |= mod.Modifier()
		if err = mod.Encode(proto.InputDispatchKeyEventTypeKeyDown, mask).Call(page); err != nil {
			return err
		}
		pressed = append(pressed, mod)
	}
	if err = key.Encode(proto.InputDispatchKeyEventTypeKeyDown, mask).Call(page); err != nil {
		return err
	}
	pressed = append(pressed, key)
	return nil
}

func cmdKey(args []string) {
	if len(args) != 1 {
		fatal("usage: rodney key <key-or-chord>")
	}
	mods, key, err := parseKeyChord(args[0])
	if err != nil {
		fatal("%v", err)
	}
	_, _, page := withPage()
	if err := sendKey(page, mods, key); err != nil {
		fatal("key failed: %v", err)
	}
	fmt.Println("Key sent")
}

func parseTypeArgs(args []string) (string, time.Duration, error) {
	fs := flag.NewFlagSet("type", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	delayText := fs.String("delay", "0", "")
	positional, err := parseInterleaved(fs, args)
	if err != nil || len(positional) != 1 {
		return "", 0, errors.New("usage: rodney type [--delay ms] <text>")
	}
	ms, err := strconv.ParseInt(*delayText, 10, 64)
	if err != nil || ms < 0 || ms > math.MaxInt64/int64(time.Millisecond) {
		return "", 0, errors.New("delay must be a nonnegative integer in milliseconds")
	}
	return positional[0], time.Duration(ms) * time.Millisecond, nil
}

type typingStroke struct {
	Key   input.Key
	Shift bool
}

func asciiTypingKey(cluster string) (typingStroke, bool) {
	if len(cluster) != 1 || cluster[0] < ' ' || cluster[0] > '~' {
		return typingStroke{}, false
	}
	c := cluster[0]
	if c >= 'A' && c <= 'Z' {
		return typingStroke{input.Key(c), true}, true
	}
	if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
		return typingStroke{input.Key(c), false}, true
	}
	for _, base := range "`1234567890-=[]\\;',./" {
		shifted, ok := input.Key(base).Shift()
		if shifted == input.Key(c) && ok {
			return typingStroke{shifted, true}, true
		}
	}
	if c == ' ' {
		return typingStroke{input.Space, false}, true
	}
	return typingStroke{input.Key(c), false}, true
}

func typeClusters(value string) []string {
	g := uniseg.NewGraphemes(value)
	var clusters []string
	for g.Next() {
		clusters = append(clusters, g.Str())
	}
	return clusters
}

func editableFocus(page *rod.Page) error {
	result, err := page.Eval(`() => {
	 const el = document.activeElement;
	 if (!el || el === document.body || el === document.documentElement) return false;
	 if (el.isContentEditable) return true;
	 if (el.matches('textarea')) return !el.disabled && !el.readOnly;
	 if (!el.matches('input')) return false;
	 return !el.disabled && !el.readOnly &&
	   ['text','search','email','url','tel','password'].includes(el.type);
	}`)
	if err != nil {
		return errors.New("cannot inspect focused element")
	}
	if !result.Value.Bool() {
		return errors.New("focused element is not editable text")
	}
	return nil
}

func insertText(page *rod.Page, value string) error {
	if err := (proto.InputInsertText{Text: value}).Call(page); err != nil {
		return errors.New("text insertion failed")
	}
	return nil
}

func typeText(page *rod.Page, clusters []string, delay time.Duration) error {
	for i, cluster := range clusters {
		if i > 0 {
			time.Sleep(delay)
		}
		if stroke, ok := asciiTypingKey(cluster); ok {
			var mods []input.Key
			if stroke.Shift {
				mods = []input.Key{input.ShiftLeft}
			}
			if err := sendKey(page, mods, stroke.Key); err != nil {
				return errors.New("typing failed")
			}
		} else if err := insertText(page, cluster); err != nil {
			return err
		}
	}
	return nil
}

func cmdType(args []string) {
	value, delay, err := parseTypeArgs(args)
	if err != nil {
		fatal("%v", err)
	}
	_, _, page := withPage()
	if err := editableFocus(page); err != nil {
		fatal("%v", err)
	}
	clusters := typeClusters(value)
	var duration time.Duration
	if delay > 0 && len(clusters) > 1 {
		if int64(len(clusters)-1) > math.MaxInt64/int64(delay) {
			duration = time.Duration(math.MaxInt64)
		} else {
			duration = time.Duration(len(clusters)-1) * delay
		}
	}
	page = timedPage(page, duration)
	if err := typeText(page, clusters, delay); err != nil {
		fatal("%v", err)
	}
	fmt.Println("Typed")
}
