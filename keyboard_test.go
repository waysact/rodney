package main

import (
	"bytes"
	"log"
	"reflect"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
)

func TestParseKeyChord(t *testing.T) {
	good := []struct {
		name string
		mods []input.Key
		key  input.Key
	}{
		{"control+K", []input.Key{input.ControlLeft}, input.Key('k')},
		{"META+k", []input.Key{input.MetaLeft}, input.Key('k')},
		{"Shift+ArrowLeft", []input.Key{input.ShiftLeft}, input.ArrowLeft},
		{"Shift+K", []input.Key{input.ShiftLeft}, input.Key('K')},
		{"Shift+1", []input.Key{input.ShiftLeft}, input.Key('!')},
		{"Enter", nil, input.Enter},
		{"Tab", nil, input.Tab},
		{"Escape", nil, input.Escape},
		{"ArrowUp", nil, input.ArrowUp},
		{"ArrowDown", nil, input.ArrowDown},
		{"ArrowLeft", nil, input.ArrowLeft},
		{"ArrowRight", nil, input.ArrowRight},
	}
	for _, tc := range good {
		t.Run(tc.name, func(t *testing.T) {
			mods, key, err := parseKeyChord(tc.name)
			if err != nil || !reflect.DeepEqual(mods, tc.mods) || key != tc.key {
				t.Fatalf("mods=%v key=%v err=%v", mods, key, err)
			}
		})
	}
	for _, chord := range []string{"K", "Control", "Control+Control+K", "Primary+K", "Control+F1", "Control++K", "Control+K+L", ""} {
		t.Run("bad/"+chord, func(t *testing.T) {
			if _, _, err := parseKeyChord(chord); err == nil {
				t.Fatal("accepted invalid chord")
			}
		})
	}
}

func TestInsertTextTraceOmitsValue(t *testing.T) {
	u := newLauncher("1").MustLaunch()
	var trace bytes.Buffer
	browser := rod.New().ControlURL(u).NoDefaultDevice().Trace(true).Logger(log.New(&trace, "", 0)).MustConnect()
	defer browser.MustClose()
	page := browser.MustPage(env.server.URL + "/fixture/keyboard")
	page.MustElement("#edit").MustFocus()
	const marker = "private_trace_probe_97"
	if err := insertText(page, marker); err != nil {
		t.Fatal(err)
	}
	if got := page.MustEval(`() => document.querySelector('#edit').value`).Str(); got != marker {
		t.Fatalf("value=%q", got)
	}
	if bytes.Contains(trace.Bytes(), []byte(marker)) {
		t.Fatal("entered text appeared in trace")
	}
}

func TestParseTypeArgs(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		text  string
		delay time.Duration
	}{
		{[]string{"hello"}, "hello", 0},
		{[]string{"--delay", "12", "two words"}, "two words", 12 * time.Millisecond},
		{[]string{"two words", "--delay", "7"}, "two words", 7 * time.Millisecond},
	} {
		got, delay, err := parseTypeArgs(tc.args)
		if err != nil || got != tc.text || delay != tc.delay {
			t.Fatalf("args=%q text=%q delay=%v err=%v", tc.args, got, delay, err)
		}
	}
	for _, args := range [][]string{nil, {"one", "two"}, {"--bogus", "one"}, {"--delay", "-1", "one"}, {"--delay", "NaN", "one"}} {
		if _, _, err := parseTypeArgs(args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}

func TestTypeClusters(t *testing.T) {
	got := typeClusters("A" + "e\u0301" + "👩‍💻")
	if !reflect.DeepEqual(got, []string{"A", "e\u0301", "👩‍💻"}) {
		t.Fatalf("clusters=%q", got)
	}
	for _, tc := range []struct {
		char  string
		shift bool
	}{{"A", true}, {"!", true}, {"a", false}, {"1", false}} {
		stroke, ok := asciiTypingKey(tc.char)
		if !ok || stroke.Shift != tc.shift {
			t.Fatalf("%q: %+v, %v", tc.char, stroke, ok)
		}
	}
	if _, ok := asciiTypingKey("e\u0301"); ok {
		t.Fatal("accepted multi-rune cluster")
	}
}
