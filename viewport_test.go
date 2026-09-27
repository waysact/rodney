package main

import (
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

func TestParseViewport(t *testing.T) {
	good := map[string]Viewport{"1280x800": {1280, 800}, "900X600": {900, 600}}
	for in, want := range good {
		got, err := parseViewport(in)
		if err != nil || got != want {
			t.Errorf("parseViewport(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "1280", "x800", "0x800", "1280x-1", "axb", "1280x800x2"} {
		if _, err := parseViewport(in); err == nil {
			t.Errorf("parseViewport(%q) returned no error", in)
		}
	}
}

func TestApplyViewport_ExactSize(t *testing.T) {
	page := navigateTo(t, "/fixture/plain")
	for _, want := range []Viewport{{900, 600}, {1100, 700}} {
		if err := applyViewport(page, want); err != nil {
			t.Fatalf("applyViewport(%v): %v", want, err)
		}
		got, err := pageViewport(page)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("viewport = %v, want %v", got, want)
		}
	}
}

func TestApplyViewport_ClearsOverride(t *testing.T) {
	page := navigateTo(t, "/fixture/plain")
	if err := (proto.EmulationSetDeviceMetricsOverride{Width: 500, Height: 400}).Call(page); err != nil {
		t.Fatal(err)
	}
	want := Viewport{900, 600}
	if err := applyViewport(page, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := pageViewport(page); got != want {
		t.Fatalf("viewport = %v, want %v", got, want)
	}
}
