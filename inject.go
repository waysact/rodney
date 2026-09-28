package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// InjectEntry is one injected script. Its source is stored in injectDir.
type InjectEntry struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

var injectNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func injectDir() string { return filepath.Join(stateDir(), "inject") }

func injectPath(name string) string { return filepath.Join(injectDir(), name+".js") }

func hashSource(src string) string {
	sum := sha256.Sum256([]byte(src))
	return hex.EncodeToString(sum[:])
}

// wrapInject runs a script once per document and version, in the top frame.
// Its function scope keeps declarations separate from js --file scripts.
// Scripts export through window, and this is window.
func wrapInject(name, hash, src string) string {
	key, _ := json.Marshal(name + ":" + hash)
	return "(function () { if (window !== window.top) return; const r = (window.__rodneyInjected ??= {}); if (r[" +
		string(key) + "]) return; const entry = (r[" + string(key) + "] = {}); try {\n" +
		"(function () {\n" + src + "\n}).call(window);\n" +
		"} catch (error) { entry.error = 'script threw'; try { entry.error = String(error && (error.stack || error)) || '(empty error)'; } catch {} throw error; }\n}).call(window);"
}

// injectError reads a stored exception. mark prevents a second warning for
// the same document; add leaves it unmarked so every retry still fails.
func injectError(page *rod.Page, name, hash string, mark bool) (string, error) {
	key, _ := json.Marshal(name + ":" + hash)
	expr := fmt.Sprintf(`(() => { const entry = window.__rodneyInjected?.[%s];
		if (!entry?.error || (%t && entry.warned)) return "";
		if (%t) entry.warned = true;
		return entry.error; })()`, key, mark, mark)
	res, err := proto.RuntimeEvaluate{Expression: expr, ReturnByValue: true}.Call(page)
	if err != nil {
		return "", err
	}
	if res.ExceptionDetails != nil {
		return "", fmt.Errorf("failed to inspect injected script %q: %s", name, exceptionText(res.ExceptionDetails))
	}
	return res.Result.Value.Str(), nil
}

// applyInjects registers scripts for new documents and runs them in the
// current document. A script exception warns; a Chrome failure is an error.
func applyInjects(page *rod.Page, s *State, warn io.Writer) error {
	for _, e := range s.Injects {
		src, err := os.ReadFile(injectPath(e.Name))
		if err != nil {
			return fmt.Errorf("failed to read injected script %q: %w", e.Name, err)
		}
		wrapped := wrapInject(e.Name, e.Hash, string(src))
		if _, err := (proto.PageAddScriptToEvaluateOnNewDocument{Source: wrapped}).Call(page); err != nil {
			return fmt.Errorf("failed to register injected script %q: %w", e.Name, err)
		}
		res, err := proto.RuntimeEvaluate{Expression: wrapped}.Call(page)
		if err != nil {
			return fmt.Errorf("failed to run injected script %q: %w", e.Name, err)
		}
		message, err := injectError(page, e.Name, e.Hash, true)
		if err != nil {
			return err
		}
		if message == "" && res.ExceptionDetails != nil {
			message = exceptionText(res.ExceptionDetails)
		}
		if message != "" {
			fmt.Fprintf(warn, "warning: injected script %q threw: %s\n", e.Name, message)
		}
	}
	return nil
}

// injectAdd runs a script in the active page and stores a snapshot if it
// succeeds. Adding the same name replaces its script in the same position.
func injectAdd(page *rod.Page, s *State, name, src string) error {
	unlock, err := lockRecordingMutation()
	if err != nil {
		return err
	}
	defer unlock()
	if !injectNameRe.MatchString(name) {
		return fmt.Errorf("invalid script name %q (use letters, digits, - and _)", name)
	}
	hash := hashSource(src)
	res, err := proto.RuntimeEvaluate{Expression: wrapInject(name, hash, src)}.Call(page)
	if err != nil {
		return err
	}
	if res.ExceptionDetails != nil {
		return fmt.Errorf("script threw: %s", exceptionText(res.ExceptionDetails))
	}
	if message, err := injectError(page, name, hash, false); err != nil {
		return err
	} else if message != "" {
		return fmt.Errorf("script threw: %s", message)
	}
	current, err := updateState(func(current *State) error {
		if err := os.MkdirAll(injectDir(), 0700); err != nil {
			return err
		}
		if err := os.Chmod(injectDir(), 0700); err != nil {
			return err
		}
		f, err := os.CreateTemp(injectDir(), ".inject-*.js")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err := f.Write([]byte(src)); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if err := os.Rename(f.Name(), injectPath(name)); err != nil {
			return err
		}
		entry := InjectEntry{Name: name, Hash: hash}
		for i, e := range current.Injects {
			if e.Name == name {
				current.Injects[i] = entry
				return nil
			}
		}
		current.Injects = append(current.Injects, entry)
		return nil
	})
	if err == nil {
		*s = *current
	}
	return err
}

// injectRemove stops future injection. Existing documents keep the script.
func injectRemove(s *State, name string) error {
	unlock, err := lockRecordingMutation()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := updateState(func(current *State) error {
		for i, e := range current.Injects {
			if e.Name == name {
				if err := os.Remove(injectPath(name)); err != nil && !os.IsNotExist(err) {
					return err
				}
				current.Injects = append(current.Injects[:i], current.Injects[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("no injected script named %q", name)
	})
	if err == nil {
		*s = *current
	}
	return err
}

func injectClear(s *State) error {
	unlock, err := lockRecordingMutation()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := updateState(func(current *State) error {
		if err := os.RemoveAll(injectDir()); err != nil {
			return err
		}
		current.Injects = nil
		return nil
	})
	if err == nil {
		*s = *current
	}
	return err
}

func cmdInject(args []string) {
	const usage = "usage: rodney inject add <name> <file|->\n       rodney inject remove <name>\n       rodney inject clear\n       rodney inject list"
	if len(args) < 1 {
		fatal("%s", usage)
	}
	switch args[0] {
	case "add":
		if len(args) != 3 {
			fatal("%s", usage)
		}
		src, err := readScriptSource(args[2])
		if err != nil {
			fatal("%v", err)
		}
		s, _, page := withPage()
		if err := injectAdd(page, s, args[1], src); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("Injected %s\n", args[1])
	case "remove":
		if len(args) != 2 {
			fatal("%s", usage)
		}
		s, err := loadState()
		if err != nil {
			fatal("%v", err)
		}
		if err := injectRemove(s, args[1]); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("Removed %s\n", args[1])
	case "clear":
		if len(args) != 1 {
			fatal("%s", usage)
		}
		s, err := loadState()
		if err != nil {
			fatal("%v", err)
		}
		if err := injectClear(s); err != nil {
			fatal("%v", err)
		}
		fmt.Println("Cleared injected scripts")
	case "list":
		if len(args) != 1 {
			fatal("%s", usage)
		}
		s, err := loadState()
		if err != nil {
			fatal("%v", err)
		}
		for _, e := range s.Injects {
			fmt.Printf("%s %s\n", e.Name, e.Hash[:12])
		}
	default:
		fatal("%s", usage)
	}
}
