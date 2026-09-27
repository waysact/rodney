package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
)

// evalScript runs src at global scope, as the DevTools console does, and
// returns the completion value. It does not wrap src in a function, so
// top-level var and function declarations become globals and an IIFE or
// UMD build works as it does from a script tag. REPL mode permits
// top-level await and lets a file with let or const run again.
func evalScript(page *rod.Page, src string) (*proto.RuntimeRemoteObject, error) {
	res, err := proto.RuntimeEvaluate{
		Expression:    src,
		ReplMode:      true,
		AwaitPromise:  true,
		ReturnByValue: true,
	}.Call(page)
	if err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		return nil, errors.New(exceptionText(res.ExceptionDetails))
	}
	return res.Result, nil
}

func exceptionText(d *proto.RuntimeExceptionDetails) string {
	if d.Exception != nil && d.Exception.Description != "" {
		return d.Exception.Description
	}
	return d.Text
}

// readScriptSource reads a script file, or stdin when path is "-".
func readScriptSource(path string) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("failed to read script: %w", err)
	}
	return string(data), nil
}

// printJSValue prints a JS value the way "rodney js" always has.
func printJSValue(v gson.JSON) {
	raw := v.JSON("", "")
	// For simple types, print cleanly; for objects/arrays, pretty-print.
	switch {
	case raw == "null" || raw == "undefined":
		fmt.Println(raw)
	case raw == "true" || raw == "false":
		fmt.Println(raw)
	case len(raw) > 0 && raw[0] == '"':
		fmt.Println(v.Str())
	case len(raw) > 0 && (raw[0] == '{' || raw[0] == '['):
		fmt.Println(v.JSON("", "  "))
	default:
		fmt.Println(raw)
	}
}

func printJSResult(obj *proto.RuntimeRemoteObject) {
	if obj.Type == proto.RuntimeRemoteObjectTypeUndefined {
		fmt.Println("undefined")
		return
	}
	printJSValue(obj.Value)
}
