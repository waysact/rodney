package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

func recordJPEG(t *testing.T, width, height int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRecordFrameStaticAndTransition(t *testing.T) {
	start := time.Unix(0, 0)
	var frames [][]byte
	p := newFramePacer(func(data []byte) error { frames = append(frames, bytes.Clone(data)); return nil }, defaultRecordFPS)
	red := recordJPEG(t, 8, 6, color.RGBA{R: 255, A: 255})
	blue := recordJPEG(t, 8, 6, color.RGBA{B: 255, A: 255})
	if err := p.offer(red, start); err != nil {
		t.Fatal(err)
	}
	if err := p.offer(blue, start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := p.flush(start.Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 75 {
		t.Fatalf("got %d frames, want 75", len(frames))
	}
	if !bytes.Equal(frames[29], red) || !bytes.Equal(frames[30], blue) {
		t.Fatal("frame transition did not occur at 2 seconds")
	}
	if len(p.latest) != len(blue) {
		t.Fatal("pacer does not retain only the latest frame")
	}
}

func TestRecordFrameDimensionChange(t *testing.T) {
	start := time.Unix(0, 0)
	p := newFramePacer(func([]byte) error { return nil }, defaultRecordFPS)
	if err := p.offer(recordJPEG(t, 8, 6, color.White), start); err != nil {
		t.Fatal(err)
	}
	if err := p.offer(recordJPEG(t, 10, 6, color.White), start.Add(time.Second)); err == nil {
		t.Fatal("dimension change accepted")
	}
}

func TestRecordFrameQueueRejectsBackpressure(t *testing.T) {
	q := newFrameQueue(1)
	if err := q.offer([]byte("first")); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- q.offer([]byte("second")) }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("full encoder queue accepted another frame")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("full encoder queue blocked the caller")
	}
}

func TestRecordEncodeStaticMP4(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}
	file := filepath.Join(t.TempDir(), "static.mp4")
	enc, err := newRecordingEncoder(file)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(0, 0)
	p := newFramePacer(enc.write, defaultRecordFPS)
	if err := p.offer(recordJPEG(t, 9, 7, color.RGBA{R: 255, A: 255}), start); err != nil {
		t.Fatal(err)
	}
	if err := p.flush(start.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if p.next != 30 {
		t.Fatalf("wrote %d frames, want 30", p.next)
	}
	if err := enc.finish(); err != nil {
		t.Fatal(err)
	}
	video, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=pix_fmt,r_frame_rate", "-of", "default=noprint_wrappers=1", file).CombinedOutput()
	if err != nil || !strings.Contains(string(video), "pix_fmt=yuv420p") || !strings.Contains(string(video), "r_frame_rate=15/1") {
		t.Fatalf("default video format = %q: %v", video, err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nokey=1:noprint_wrappers=1", file).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe: %v: %s", err, out)
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || d < 1.9 || d > 2.1 {
		t.Fatalf("duration %q, want about 2 seconds", out)
	}
}

func TestRecordWorkerReloadKeepsInjection(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	home := startSession(t)
	t.Setenv("RODNEY_HOME", home)
	mustRodney(t, home, "open", env.server.URL+"/fixture/record-reload")
	mustRodney(t, home, "inject", "add", "presenter", writeScript(t, "window.presenterLoaded = true"))
	data, err := os.ReadFile(filepath.Join(home, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	browser, err := connectBrowser(&state)
	if err != nil {
		t.Fatal(err)
	}
	page, err := getActivePage(browser, &state)
	if err != nil {
		t.Fatal(err)
	}
	target := proto.TargetTargetID(page.TargetID)
	file := filepath.Join(t.TempDir(), "reload.mp4")
	ready := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { _, err := runRecording(&state, target, file, "", defaultRecordFPS, ready, stop); done <- err }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("recorder failed before ready: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("recorder not ready")
	}
	time.Sleep(5 * time.Second)
	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("recorder did not stop")
	}
	out, err := exec.Command("ffmpeg", "-v", "error", "-i", file, "-vf", "scale=1:1", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1").CombinedOutput()
	if err != nil {
		t.Fatalf("decode video: %v: %s", err, out)
	}
	green, red, blue := false, false, false
	for i := 0; i+2 < len(out); i += 3 {
		r, g, b := out[i], out[i+1], out[i+2]
		if g > 75 && r < 75 && b < 75 {
			green = true
		}
		if r > 160 && g < 100 && b < 100 {
			red = true
		}
		if b > 160 && r < 100 && g < 100 {
			blue = true
		}
	}
	if !green || !red || blue {
		t.Fatalf("recorded colors: green=%t red=%t blue=%t (%d samples)", green, red, blue, len(out)/3)
	}
	if _, err := exec.LookPath("ffprobe"); err == nil {
		probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nokey=1:noprint_wrappers=1", file).CombinedOutput()
		if err != nil {
			t.Fatalf("ffprobe: %v: %s", err, probe)
		}
		seconds, err := strconv.ParseFloat(strings.TrimSpace(string(probe)), 64)
		if err != nil || seconds < 4.8 || seconds > 6 {
			t.Fatalf("recorded duration %q, want about five seconds", probe)
		}
	}
}
