package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/devices"
)

// rodneyBin is the rodney binary that TestMain builds.
var rodneyBin string

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// runRodney runs the built binary with RODNEY_HOME=home, the same way a
// shell script runs separate rodney commands.
func runRodney(t *testing.T, home string, args ...string) (string, string, int) {
	t.Helper()
	return runRodneyStdin(t, home, "", args...)
}

func runRodneyStdin(t *testing.T, home, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(rodneyBin, args...)
	cmd.Env = append(os.Environ(), "RODNEY_HOME="+home)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("failed to run rodney %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// mustRodney runs rodney, fails the test on a non-zero exit, and returns
// stdout without the trailing newline.
func mustRodney(t *testing.T, home string, args ...string) string {
	t.Helper()
	out, errOut, code := runRodney(t, home, args...)
	if code != 0 {
		t.Fatalf("rodney %v exited %d: %s", args, code, errOut)
	}
	return strings.TrimSpace(out)
}

// startSession runs "rodney start" in a new RODNEY_HOME and stops the
// browser when the test ends. The directory is removed on a best-effort
// basis, because Windows can hold Chrome's files for a moment after exit.
func startSession(t *testing.T, startArgs ...string) string {
	t.Helper()
	home, err := os.MkdirTemp("", "rodney-home")
	if err != nil {
		t.Fatal(err)
	}
	mustRodney(t, home, append([]string{"start"}, startArgs...)...)
	t.Cleanup(func() {
		runRodney(t, home, "stop")
		os.RemoveAll(home)
	})
	return home
}

// eventually polls cond for up to 10 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestE2E_StartOpenTitle(t *testing.T) {
	home := startSession(t)
	if got := mustRodney(t, home, "open", env.server.URL+"/fixture/plain"); got != "Plain" {
		t.Fatalf("open printed %q, want %q", got, "Plain")
	}
	if got := mustRodney(t, home, "title"); got != "Plain" {
		t.Fatalf("title printed %q, want %q", got, "Plain")
	}
}

func writeScript(t *testing.T, src string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "inject.js")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestE2E_InjectSurvivesReload(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	script := writeScript(t, "window.presenterLoaded = (window.presenterLoaded || 0) + 1;")
	mustRodney(t, home, "inject", "add", "presenter", script)
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if got := mustRodney(t, home, "js", "window.presenterLoaded"); got != "1" {
			t.Fatalf("command %d: presenterLoaded = %s, want 1", i, got)
		}
	}
	mustRodney(t, home, "reload")
	if got := mustRodney(t, home, "js", "window.presenterLoaded"); got != "1" {
		t.Fatalf("after reload: presenterLoaded = %s, want 1", got)
	}
	origin := mustRodney(t, home, "js", "performance.timeOrigin")
	mustRodney(t, home, "js", "setTimeout(() => location.reload(), 1000)")
	eventually(t, "a new document after the scheduled reload", func() bool {
		// A command can race document replacement; retry transient errors.
		out, _, code := runRodney(t, home, "js", "performance.timeOrigin")
		return code == 0 && strings.TrimSpace(out) != "" && strings.TrimSpace(out) != origin
	})
	if got := mustRodney(t, home, "js", "window.presenterLoaded"); got != "1" {
		t.Fatalf("after scheduled reload: presenterLoaded = %s, want 1", got)
	}
}

func TestE2E_ScreenshotTimeoutRestoresViewport(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	t.Setenv("ROD_TIMEOUT", "2")
	out, errOut, code := runRodney(t, home, "screenshot-el", "#missing", "-w", "800", "-h", "600", filepath.Join(t.TempDir(), "missing.png"))
	if code != 2 || out != "" || !strings.Contains(errOut, "context deadline exceeded") {
		t.Fatalf("capture: stdout %q, stderr %q, exit %d; want timeout and exit 2", out, errOut, code)
	}
	// This command uses a new CDP connection, with no viewport correction.
	if got := mustRodney(t, home, "js", `innerWidth + "x" + innerHeight`); got != "1280x800" {
		t.Errorf("viewport after timeout = %s, want 1280x800", got)
	}
	if strings.Contains(errOut, "failed to clear viewport override") {
		t.Errorf("override cleanup failed: %s", errOut)
	}
}

func TestE2E_ScreenshotRejectsNonfiniteOptions(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/elements")
	for _, tc := range []struct{ command, option string }{
		{"screenshot", "scale"},
		{"screenshot-el", "scale"},
		{"screenshot-el", "margin"},
	} {
		for _, value := range []string{"NaN", "+Inf", "-Inf"} {
			t.Run(tc.command+"/"+tc.option+"/"+value, func(t *testing.T) {
				file := filepath.Join(t.TempDir(), "invalid.png")
				args := []string{tc.command, "--" + tc.option, value}
				if tc.command == "screenshot-el" {
					args = append(args, "#target")
				}
				out, errOut, code := runRodney(t, home, append(args, file)...)
				if code != 2 || out != "" || !strings.Contains(errOut, "finite") || strings.Contains(errOut, "panic:") {
					t.Errorf("stdout %q, stderr %q, exit %d; want finite-option error and exit 2", out, errOut, code)
				}
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Errorf("invalid option created an output file: %v", err)
				}
			})
		}
	}
}

func TestE2E_InjectVisibleToPageScripts(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	mustRodney(t, home, "inject", "add", "presenter", writeScript(t, "window.presenterLoaded = true;"))
	if got := mustRodney(t, home, "open", env.server.URL+"/fixture/inject-probe"); got != "seen" {
		t.Fatalf("open: page scripts saw %q, want seen", got)
	}
	mustRodney(t, home, "newpage", env.server.URL+"/fixture/inject-probe")
	if got := mustRodney(t, home, "title"); got != "seen" {
		t.Fatalf("newpage: page scripts saw %q, want seen", got)
	}
}

func TestE2E_InjectAddChangedConst(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	mustRodney(t, home, "inject", "add", "presenter", writeScript(t, "const version = 1; window.presenterVersion = version;"))
	mustRodney(t, home, "inject", "add", "presenter", writeScript(t, "const version = 2; window.presenterVersion = version;"))
	if got := mustRodney(t, home, "js", "window.presenterVersion"); got != "2" {
		t.Fatalf("presenterVersion = %s, want 2", got)
	}
	if got := mustRodney(t, home, "inject", "list"); strings.Count(got, "presenter") != 1 {
		t.Fatalf("inject list = %q, want one presenter entry", got)
	}
}

func TestE2E_InjectAddThrowing(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	_, errOut, code := runRodney(t, home, "inject", "add", "bad", writeScript(t, "throw new Error('bad script');"))
	if code != 2 || !strings.Contains(errOut, "bad script") {
		t.Fatalf("inject add exited %d with %q, want 2 and the error", code, errOut)
	}
	if got := mustRodney(t, home, "inject", "list"); got != "" {
		t.Fatalf("inject list = %q, want empty", got)
	}
}

func TestE2E_InjectAddThrowingTwice(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"error", "throw new Error('repeat failure');"},
		{"empty string", "throw '';"},
		{"throwing stack getter", "throw { get stack() { throw new Error('stack getter'); } };"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := startSession(t)
			mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
			script := writeScript(t, tc.source)
			for i := 0; i < 2; i++ {
				_, errOut, code := runRodney(t, home, "inject", "add", "bad", script)
				if code != 2 || !strings.Contains(errOut, "script threw") {
					t.Fatalf("attempt %d exited %d with %q, want 2 and error", i+1, code, errOut)
				}
			}
			if got := mustRodney(t, home, "inject", "list"); got != "" {
				t.Fatalf("inject list = %q, want empty", got)
			}
		})
	}
}

func TestE2E_InjectWarnsAfterNewDocumentFailure(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	script := writeScript(t, `if (location.pathname === "/fixture/throws") throw new Error("new document failure");`)
	mustRodney(t, home, "inject", "add", "fragile", script)
	warnings := 0
	for _, args := range [][]string{
		{"open", env.server.URL + "/fixture/throws"},
		{"title"},
		{"title"},
	} {
		_, errOut, code := runRodney(t, home, args...)
		if code != 0 {
			t.Fatalf("rodney %v exited %d: %s", args, code, errOut)
		}
		warnings += strings.Count(errOut, `injected script "fragile" threw`)
	}
	if warnings != 1 {
		t.Fatalf("got %d warnings for one document, want 1", warnings)
	}
}

func TestE2E_InjectWarnsOncePerDocument(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	src := `if (location.pathname === "/fixture/throws") throw new Error("injected failure");`
	mustRodney(t, home, "inject", "add", "fragile", writeScript(t, src))
	mustRodney(t, home, "open", env.server.URL+"/fixture/throws")
	mustRodney(t, home, "js", "setTimeout(() => location.reload(), 1000)")
	warnings := 0
	eventually(t, "a warning about the injected script", func() bool {
		_, errOut, code := runRodney(t, home, "title")
		if code != 0 {
			t.Fatalf("title exited %d: %s", code, errOut)
		}
		if strings.Contains(errOut, `injected script "fragile" threw`) {
			warnings++
		}
		return warnings > 0
	})
	_, errOut, _ := runRodney(t, home, "title")
	if strings.Contains(errOut, "fragile") {
		t.Fatalf("second command warned again: %s", errOut)
	}
}

func TestE2E_InjectRemoveClearList(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	mustRodney(t, home, "inject", "add", "one", writeScript(t, "window.one = 1;"))
	mustRodney(t, home, "inject", "add", "two", writeScript(t, "window.two = 2;"))
	lines := strings.Split(mustRodney(t, home, "inject", "list"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "one ") || !strings.HasPrefix(lines[1], "two ") {
		t.Fatalf("inject list = %q, want one and two in order", lines)
	}
	mustRodney(t, home, "inject", "remove", "one")
	if got := mustRodney(t, home, "inject", "list"); !strings.HasPrefix(got, "two ") || strings.Contains(got, "one") {
		t.Fatalf("after remove: %q", got)
	}
	if _, _, code := runRodney(t, home, "inject", "remove", "one"); code != 2 {
		t.Fatalf("removing a missing script exited %d, want 2", code)
	}
	mustRodney(t, home, "inject", "clear")
	if got := mustRodney(t, home, "inject", "list"); got != "" {
		t.Fatalf("after clear: %q", got)
	}
	mustRodney(t, home, "inject", "add", "three", writeScript(t, "window.three = 3;"))
	mustRodney(t, home, "stop")
	if _, err := os.Stat(filepath.Join(home, "inject")); !os.IsNotExist(err) {
		t.Fatalf("inject directory still exists after stop: %v", err)
	}
}

func TestE2E_JSFile(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	file := filepath.Join(t.TempDir(), "script.js")
	if err := os.WriteFile(file, []byte("var fromFile = 'loaded';\nfromFile + '!'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := mustRodney(t, home, "js", "--file", file); got != "loaded!" {
		t.Fatalf("js --file printed %q, want loaded!", got)
	}
	if got := mustRodney(t, home, "js", "window.fromFile"); got != "loaded" {
		t.Fatalf("global from file = %q, want loaded", got)
	}
	out, errOut, code := runRodneyStdin(t, home, "[1, 2].length", "js", "--file", "-")
	if code != 0 || strings.TrimSpace(out) != "2" {
		t.Fatalf("js --file - exited %d with %q %q, want 2", code, out, errOut)
	}
	if got := mustRodney(t, home, "js", "--file", file); got != "loaded!" {
		t.Fatalf("second run printed %q", got)
	}
	if _, _, code := runRodney(t, home, "js", "--file", file, "extra"); code != 2 {
		t.Fatalf("js --file with an expression exited %d, want 2", code)
	}
	if got := mustRodney(t, home, "js", "1 + 2"); got != "3" {
		t.Fatalf("bare js printed %q, want 3", got)
	}
}

func TestE2E_NoResizeAcrossCommands(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/resize-counter")
	var got string
	for i := 0; i < 10; i++ {
		got = mustRodney(t, home, "js", "window.resizes")
	}
	if got != "0" {
		t.Fatalf("page saw %s resize events across ten commands, want 0", got)
	}
}

func TestE2E_UserAgentKept(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	want := devices.LaptopWithMDPIScreen.UserAgentEmulation()
	if got := mustRodney(t, home, "js", "navigator.userAgent"); got != want.UserAgent {
		t.Errorf("navigator.userAgent = %q, want %q", got, want.UserAgent)
	}
	if got := mustRodney(t, home, "js", `navigator.languages.join(",")`); got != want.AcceptLanguage {
		t.Errorf("navigator.languages = %q, want %q", got, want.AcceptLanguage)
	}
}

const viewportExpr = `[innerWidth, innerHeight, devicePixelRatio].join("x")`

func TestE2E_DefaultViewport(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	if got := mustRodney(t, home, "js", viewportExpr); got != "1280x800x1" {
		t.Fatalf("viewport = %s, want 1280x800x1", got)
	}
}

func TestE2E_StartViewportAndScale(t *testing.T) {
	home := startSession(t, "--viewport", "900x600", "--scale", "2")
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	for i := 0; i < 3; i++ {
		if got := mustRodney(t, home, "js", viewportExpr); got != "900x600x2" {
			t.Fatalf("command %d: viewport = %s, want 900x600x2", i, got)
		}
	}
	mustRodney(t, home, "newpage", env.server.URL+"/fixture/plain")
	if got := mustRodney(t, home, "js", viewportExpr); got != "900x600x2" {
		t.Fatalf("new page: viewport = %s, want 900x600x2", got)
	}
}

func TestE2E_ViewportCommand(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	if got := mustRodney(t, home, "viewport"); got != "1280x800" {
		t.Fatalf("viewport printed %q, want 1280x800", got)
	}
	if got := mustRodney(t, home, "viewport", "1000x700"); got != "1000x700" {
		t.Fatalf("viewport 1000x700 printed %q", got)
	}
	if got := mustRodney(t, home, "js", viewportExpr); got != "1000x700x1" {
		t.Fatalf("viewport = %s, want 1000x700x1", got)
	}
	mustRodney(t, home, "newpage", env.server.URL+"/fixture/plain")
	if got := mustRodney(t, home, "viewport"); got != "1000x700" {
		t.Fatalf("new page viewport = %q, want 1000x700", got)
	}
}

func TestE2E_ScreenshotOverrideDoesNotPersist(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/tall")
	file := filepath.Join(t.TempDir(), "small.png")
	if got := mustRodney(t, home, "screenshot", "-w", "800", "-h", "600", file); got != file {
		t.Fatalf("screenshot printed %q, want %q", got, file)
	}
	if got := mustRodney(t, home, "viewport"); got != "1280x800" {
		t.Fatalf("viewport after screenshot = %s, want 1280x800", got)
	}
}

func TestE2E_ScreenshotViewportAndScale(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/tall")
	file := filepath.Join(t.TempDir(), "shot.png")
	mustRodney(t, home, "screenshot", "-h", "800", "--scale", "2", file)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	assertSize(t, decodePNG(t, data), 2560, 1600)
}

func TestE2E_ScreenshotFrame(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/tall")
	file := filepath.Join(t.TempDir(), "framed.png")
	mustRodney(t, home, "screenshot", "-h", "800", "--frame", file)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodePNG(t, data).Bounds().Dx(); got != 1282 {
		t.Fatalf("framed width = %d, want 1282", got)
	}
	if got := mustRodney(t, home, "pages"); strings.Count(got, "\n") != 0 {
		t.Fatalf("frame page was not closed; pages:\n%s", got)
	}
}

func TestE2E_ScreenshotElOutput(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/elements")
	file := filepath.Join(t.TempDir(), "el.png")
	out := mustRodney(t, home, "screenshot-el", "#target", "--margin", "5", file)
	if !regexp.MustCompile(`^Saved .+ \(\d+ bytes\)$`).MatchString(out) {
		t.Fatalf("output %q does not match Saved X (N bytes)", out)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	assertSize(t, decodePNG(t, data), 60, 50)
	if _, _, code := runRodney(t, home, "screenshot-el", "#target", "--frame", file); code != 2 {
		t.Fatalf("screenshot-el --frame exited %d, want 2", code)
	}
}

func TestE2E_RecordLifecycle(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/record-reload")
	mustRodney(t, home, "inject", "add", "presenter", writeScript(t, "window.presenterLoaded = true"))
	file := filepath.Join(t.TempDir(), "take.mp4")
	if got := mustRodney(t, home, "record", "start", file); got != file {
		t.Fatalf("start printed %q, want %q", got, file)
	}
	if _, _, code := runRodney(t, home, "record", "start", filepath.Join(t.TempDir(), "second.mp4")); code != 2 {
		t.Fatalf("second start exited %d, want 2", code)
	}
	if _, _, code := runRodney(t, home, "inject", "add", "other", writeScript(t, "window.other=true")); code != 2 {
		t.Fatalf("inject add during recording exited %d, want 2", code)
	}
	if _, _, code := runRodney(t, home, "inject", "remove", "presenter"); code != 2 {
		t.Fatalf("inject remove during recording exited %d, want 2", code)
	}
	if _, _, code := runRodney(t, home, "inject", "clear"); code != 2 {
		t.Fatalf("inject clear during recording exited %d, want 2", code)
	}
	time.Sleep(4 * time.Second)
	if got := mustRodney(t, home, "record", "stop"); !strings.Contains(got, file) {
		t.Fatalf("stop printed %q", got)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runRodney(t, home, "record", "stop"); code != 2 {
		t.Fatalf("second stop exited %d, want 2", code)
	}
	if got := mustRodney(t, home, "inject", "list"); !strings.Contains(got, "presenter") {
		t.Fatalf("injection changed during take: %q", got)
	}
}

func TestE2E_RecordCustomFPS(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/record-green")
	file := filepath.Join(t.TempDir(), "thirty.mp4")
	mustRodney(t, home, "record", "start", "--fps", "30", file)
	time.Sleep(1200 * time.Millisecond)
	mustRodney(t, home, "record", "stop")
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=r_frame_rate:format=duration", "-of", "default=noprint_wrappers=1", file).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "r_frame_rate=30/1") {
		t.Fatalf("recording frame rate: %s", out)
	}
	var duration float64
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "duration=") {
			duration, err = strconv.ParseFloat(strings.TrimPrefix(line, "duration="), 64)
			if err != nil {
				t.Fatalf("duration: %v", err)
			}
		}
	}
	if duration < 1 || duration > 2.2 {
		t.Fatalf("recording duration %.2fs, want about 1.2s", duration)
	}
}

func TestE2E_RecordRejectsInvalidFPS(t *testing.T) {
	home := startSession(t)
	for _, fps := range []string{"0", "31", "oops"} {
		file := filepath.Join(t.TempDir(), "invalid.mp4")
		_, stderr, code := runRodney(t, home, "record", "start", "--fps", fps, file)
		if code != 2 || !strings.Contains(stderr, "fps") {
			t.Fatalf("fps %q: exit %d, stderr %q", fps, code, stderr)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("fps %q created output: %v", fps, err)
		}
	}
}

func TestE2E_RecordRejectsExistingFile(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	file := filepath.Join(t.TempDir(), "existing.mp4")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runRodney(t, home, "record", "start", file); code != 2 {
		t.Fatalf("start on existing file exited %d, want 2", code)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}
}

func TestE2E_RodneyStopFinalizesRecording(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	file := filepath.Join(t.TempDir(), "stopped.mp4")
	mustRodney(t, home, "record", "start", file)
	time.Sleep(time.Second)
	mustRodney(t, home, "stop")
	if info, err := os.Stat(file); err != nil || info.Size() == 0 {
		t.Fatalf("stop did not finalize video: %v", err)
	}
}

func TestE2E_RecordTargetStaysOnOriginalPage(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/record-green")
	file := filepath.Join(t.TempDir(), "target.mp4")
	mustRodney(t, home, "record", "start", file)
	mustRodney(t, home, "newpage", env.server.URL+"/fixture/record-blue")
	time.Sleep(1200 * time.Millisecond)
	mustRodney(t, home, "record", "stop")
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", file, "-vf", "scale=1:1", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1").CombinedOutput()
	if err != nil {
		t.Fatalf("decode video: %v: %s", err, out)
	}
	if len(out) < 3 {
		t.Fatal("recording has no frames")
	}
	for i := 0; i+2 < len(out); i += 3 {
		if out[i+1] < 75 || out[i] > 75 || out[i+2] > 75 {
			t.Fatalf("frame %d is not original green tab: %v", i/3, out[i:i+3])
		}
	}
}

func TestE2E_RecordClosedTargetRecovers(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/record-green")
	mustRodney(t, home, "newpage", env.server.URL+"/fixture/record-blue")
	file := filepath.Join(t.TempDir(), "closed.mp4")
	mustRodney(t, home, "record", "start", file)
	mustRodney(t, home, "closepage")
	eventually(t, "recording worker to report closed page", func() bool {
		data, err := os.ReadFile(filepath.Join(home, "record.json"))
		if err != nil {
			return false
		}
		var control recordControl
		return json.Unmarshal(data, &control) == nil && control.Error != ""
	})
	if _, errOut, code := runRodney(t, home, "record", "stop"); code != 2 || !strings.Contains(errOut, "recorded page or browser closed") {
		t.Fatalf("stop after page close: exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("failed recording presented an output file: %v", err)
	}
	next := filepath.Join(t.TempDir(), "next.mp4")
	mustRodney(t, home, "record", "start", next)
	mustRodney(t, home, "record", "stop")
}

func TestE2E_RecordMissingFFmpeg(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	t.Setenv("PATH", "/nonexistent")
	if _, errOut, code := runRodney(t, home, "record", "start", filepath.Join(t.TempDir(), "missing.mp4")); code != 2 || !strings.Contains(errOut, "ffmpeg") {
		t.Fatalf("missing ffmpeg: exit %d, stderr %q", code, errOut)
	}
}

func TestE2E_RecordDimensionChangeFails(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/record-green")
	file := filepath.Join(t.TempDir(), "resized.mp4")
	mustRodney(t, home, "record", "start", file)
	mustRodney(t, home, "viewport", "1000x700")
	eventually(t, "recording worker to report frame dimension change", func() bool {
		data, err := os.ReadFile(filepath.Join(home, "record.json"))
		if err != nil {
			return false
		}
		var control recordControl
		return json.Unmarshal(data, &control) == nil && strings.Contains(control.Error, "dimensions changed")
	})
	if _, errOut, code := runRodney(t, home, "record", "stop"); code != 2 || !strings.Contains(errOut, "dimensions changed") {
		t.Fatalf("stop after resize: exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("resized take became an output file: %v", err)
	}
}

func TestE2E_RodneyStopCleansUpFailedRecording(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/record-green")
	mustRodney(t, home, "newpage", env.server.URL+"/fixture/record-blue")
	mustRodney(t, home, "record", "start", filepath.Join(t.TempDir(), "failed.mp4"))
	mustRodney(t, home, "closepage")
	eventually(t, "recording failure", func() bool {
		data, err := os.ReadFile(filepath.Join(home, "record.json"))
		if err != nil {
			return false
		}
		var control recordControl
		return json.Unmarshal(data, &control) == nil && control.Error != ""
	})
	if _, errOut, code := runRodney(t, home, "stop"); code != 2 || !strings.Contains(errOut, "recording failed") {
		t.Fatalf("rodney stop after recording failure: exit %d, stderr %q", code, errOut)
	}
	if got := mustRodney(t, home, "status"); got != "No active browser session" {
		t.Fatalf("status after failed stop = %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, "record.json")); !os.IsNotExist(err) {
		t.Fatalf("recorder metadata remains: %v", err)
	}
}

func TestE2E_RecordDeadWorkerRecovers(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/record-green")
	outDir := t.TempDir()
	first := filepath.Join(outDir, "killed.mp4")
	mustRodney(t, home, "record", "start", first)
	data, err := os.ReadFile(filepath.Join(home, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var control recordControl
	if err := json.Unmarshal(data, &control); err != nil {
		t.Fatal(err)
	}
	proc, err := os.FindProcess(control.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "dead worker endpoint", func() bool {
		_, err := requestRecorder(&control, http.MethodGet, "status", 200*time.Millisecond)
		return err != nil
	})
	next := filepath.Join(outDir, "next.mp4")
	mustRodney(t, home, "record", "start", next)
	mustRodney(t, home, "record", "stop")
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("killed take became completed output: %v", err)
	}
	temps, err := filepath.Glob(filepath.Join(outDir, ".rodney-record-*.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("orphaned temporary MP4s: %v", temps)
	}
}

func TestE2E_RodneyStopWaitsForRecorderStartup(t *testing.T) {
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/plain")
	control := recordControl{Token: "pending", Created: time.Now().Add(-14 * time.Second), Output: filepath.Join(t.TempDir(), "pending.mp4")}
	data, err := json.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "record.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runRodney(t, home, "stop"); code != 2 {
		t.Fatalf("stop while recorder starting exited %d, want 2", code)
	}
	if got := mustRodney(t, home, "status"); !strings.Contains(got, "Browser running") {
		t.Fatalf("browser closed during recorder startup: %q", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestE2E_RecordConcurrentStartAfterStaleState(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	home := startSession(t)
	mustRodney(t, home, "open", env.server.URL+"/fixture/record-green")
	stale := recordControl{Token: "stale", Created: time.Now().Add(-time.Minute), Output: filepath.Join(t.TempDir(), "stale.mp4"), Error: "dead worker"}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "record.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	const callers = 10
	outDir := t.TempDir()
	gate := make(chan struct{})
	results := make(chan int, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			_, _, code := runRodney(t, home, "record", "start", filepath.Join(outDir, fmt.Sprintf("take-%d.mp4", i)))
			results <- code
		}(i)
	}
	close(gate)
	wg.Wait()
	close(results)
	successes := 0
	for code := range results {
		if code == 0 {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("%d concurrent record starts succeeded, want exactly one", successes)
	}
	if successes > 0 {
		runRodney(t, home, "record", "stop")
	}
}
