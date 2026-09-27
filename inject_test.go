package main

import (
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

func runWrapped(t *testing.T, page *rod.Page, name, hash, src string) {
	t.Helper()
	res, err := proto.RuntimeEvaluate{Expression: wrapInject(name, hash, src)}.Call(page)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExceptionDetails != nil {
		t.Fatalf("wrapped script threw: %s", exceptionText(res.ExceptionDetails))
	}
}

func TestWrapInject_ConstDoesNotCollide(t *testing.T) {
	page := navigateTo(t, "/fixture/plain")
	if _, err := evalScript(page, "const shared = 1;"); err != nil {
		t.Fatal(err)
	}
	runWrapped(t, page, "a", "h1", "const shared = 2; window.fromInject = shared;")
	if got := page.MustEval(`() => window.fromInject`).Int(); got != 2 {
		t.Fatalf("window.fromInject = %d, want 2", got)
	}

	other := navigateTo(t, "/fixture/plain")
	runWrapped(t, other, "a", "h1", "const shared = 2;")
	if _, err := evalScript(other, "const shared = 1;"); err != nil {
		t.Fatalf("js --file after an injected const: %v", err)
	}
}

func TestWrapInject_RunsOncePerVersion(t *testing.T) {
	page := navigateTo(t, "/fixture/plain")
	src := "window.runs = (window.runs || 0) + 1;"
	runWrapped(t, page, "counter", "h1", src)
	runWrapped(t, page, "counter", "h1", src)
	if got := page.MustEval(`() => window.runs`).Int(); got != 1 {
		t.Fatalf("same version ran %d times, want 1", got)
	}
	runWrapped(t, page, "counter", "h2", src)
	if got := page.MustEval(`() => window.runs`).Int(); got != 2 {
		t.Fatalf("after a new version, runs = %d, want 2", got)
	}
}

func TestWrapInject_ThisIsWindow(t *testing.T) {
	page := navigateTo(t, "/fixture/plain")
	runWrapped(t, page, "iife", "h1", "this.driverLike = this.driverLike || {v: 1};")
	if got := page.MustEval(`() => window.driverLike.v`).Int(); got != 1 {
		t.Fatalf("window.driverLike.v = %d, want 1", got)
	}
}

func TestWrapInject_SourceMayDeclareR(t *testing.T) {
	for _, declaration := range []string{"const", "let", "var"} {
		t.Run(declaration, func(t *testing.T) {
			page := navigateTo(t, "/fixture/plain")
			runWrapped(t, page, declaration, "h1", declaration+" r = 7; window.injectR = r;")
			if got := page.MustEval(`() => window.injectR`).Int(); got != 7 {
				t.Fatalf("window.injectR = %d, want 7", got)
			}
		})
	}
}

func TestWrapInject_TopFrameOnly(t *testing.T) {
	page := navigateTo(t, "/fixture/iframe")
	src := wrapInject("mark", "h1", "window.marked = true;")
	if _, err := (proto.PageAddScriptToEvaluateOnNewDocument{Source: src}).Call(page); err != nil {
		t.Fatal(err)
	}
	page.MustReload().MustWaitLoad()
	page.MustElement("#child").MustFrame().MustWaitLoad()
	if !page.MustEval(`() => window.marked === true`).Bool() {
		t.Fatal("script did not run in the top frame")
	}
	if page.MustEval(`() => document.getElementById("child").contentWindow.marked === true`).Bool() {
		t.Fatal("script ran in the iframe")
	}
}
