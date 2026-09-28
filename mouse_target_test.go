package main

import (
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

func TestParseAnchor(t *testing.T) {
	x, y, err := parseAnchor("30%,70%")
	if err != nil || x != .3 || y != .7 {
		t.Fatalf("x=%v y=%v err=%v", x, y, err)
	}
	for _, value := range []string{"30,70", "-1%,20%", "101%,20%", "NaN%,20%", "20%,+Inf%", "20%"} {
		if _, _, err := parseAnchor(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}

func TestParseMouseMove(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		target   string
		duration time.Duration
		button   proto.InputMouseButton
		x, y     float64
	}{
		{[]string{"viewport"}, "viewport", 500 * time.Millisecond, proto.InputMouseButtonNone, .5, .5},
		{[]string{"--duration", "0", "#target"}, "#target", 0, proto.InputMouseButtonNone, .5, .5},
		{[]string{"#target", "--anchor", "30%,70%", "--button", "left"}, "#target", 500 * time.Millisecond, proto.InputMouseButtonLeft, .3, .7},
		{[]string{"10,20", "--duration", "25"}, "10,20", 25 * time.Millisecond, proto.InputMouseButtonNone, .5, .5},
	} {
		got, err := parseMouseMoveArgs(tc.args)
		if err != nil || got.Target != tc.target || got.Duration != tc.duration || got.Button != tc.button || got.AnchorX != tc.x || got.AnchorY != tc.y {
			t.Fatalf("args=%q got=%+v err=%v", tc.args, got, err)
		}
	}
	for _, args := range [][]string{nil, {"#a", "#b"}, {"--bogus", "#a"}, {"--duration", "-1", "#a"}, {"--duration", "NaN", "#a"}, {"--duration", "999999999999999999999", "#a"}, {"--button", "left", "--duration", "0", "#a"}, {"--anchor", "30%,70%", "10,20"}, {"1,NaN"}, {"NaN,20"}, {"Infinity,20"}} {
		if _, err := parseMouseMoveArgs(args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}
