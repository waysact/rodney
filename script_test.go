package main

import (
	"strings"
	"testing"
)

func TestEvalScript_VarBecomesGlobal(t *testing.T) {
	page := navigateTo(t, "/fixture/plain")
	if _, err := evalScript(page, "var answerFromFile = 42;"); err != nil {
		t.Fatal(err)
	}
	if got := page.MustEval(`() => window.answerFromFile`).Int(); got != 42 {
		t.Fatalf("window.answerFromFile = %d, want 42", got)
	}
}

func TestEvalScript_ThisIsWindow(t *testing.T) {
	page := navigateTo(t, "/fixture/plain")
	if _, err := evalScript(page, "this.fromThis = this.fromThis || 1;"); err != nil {
		t.Fatal(err)
	}
	if got := page.MustEval(`() => window.fromThis`).Int(); got != 1 {
		t.Fatalf("window.fromThis = %d, want 1", got)
	}
}

func TestEvalScript_TopLevelAwait(t *testing.T) {
	page := navigateTo(t, "/fixture/plain")
	obj, err := evalScript(page, "await new Promise(r => setTimeout(r, 10)); 7")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Value.Int() != 7 {
		t.Fatalf("result = %v, want 7", obj.Value)
	}
}

func TestEvalScript_LetRunsTwice(t *testing.T) {
	page := navigateTo(t, "/fixture/plain")
	for i := 0; i < 2; i++ {
		if _, err := evalScript(page, "let counter = 1; counter"); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
}

func TestEvalScript_Exception(t *testing.T) {
	page := navigateTo(t, "/fixture/plain")
	_, err := evalScript(page, "throw new Error('boom')")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want an error that contains boom", err)
	}
}
