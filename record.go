package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod/lib/proto"
	"github.com/gofrs/flock"
)

type recordControl struct {
	Token   string    `json:"token"`
	FPS     int       `json:"fps"`
	Port    int       `json:"port"`
	PID     int       `json:"pid"`
	Output  string    `json:"output"`
	Temp    string    `json:"temp"`
	Created time.Time `json:"created"`
	Error   string    `json:"error,omitempty"`
}

type recordResponse struct {
	Ready    bool    `json:"ready"`
	Output   string  `json:"output,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	Error    string  `json:"error,omitempty"`
}

var errRecordUnconfirmed = fmt.Errorf("recorder has not stopped")

func recordPath() string { return filepath.Join(stateDir(), "record.json") }

func recordLockPath() string { return filepath.Join(stateDir(), "record.lock") }

// The OS releases this lock if a process exits during a reservation or mutation.
func lockRecord() (func(), error) {
	if err := os.MkdirAll(stateDir(), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(recordLockPath())
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	ok, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("lock recorder state: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("timed out waiting for recorder state lock")
	}
	return func() { lock.Unlock() }, nil
}

func readRecordControl() (*recordControl, error) {
	data, err := os.ReadFile(recordPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c recordControl
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("invalid recorder state: %w", err)
	}
	return &c, nil
}

func writeRecordControl(c *recordControl) error {
	unlock, err := lockRecord()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := readRecordControl()
	if err != nil {
		return err
	}
	if current == nil || current.Token != c.Token {
		return fmt.Errorf("recorder state is no longer owned by this worker")
	}
	return writeRecordControlLocked(c)
}

func writeRecordControlLocked(c *recordControl) error {
	f, err := os.CreateTemp(stateDir(), ".record-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), recordPath())
}

func removeRecordTemp(c *recordControl) {
	if c.Temp == "" || filepath.Dir(c.Temp) != filepath.Dir(c.Output) || !strings.HasPrefix(filepath.Base(c.Temp), ".rodney-record-") || filepath.Ext(c.Temp) != ".mp4" {
		return
	}
	os.Remove(c.Temp)
}

func removeRecordControl(c *recordControl) error {
	unlock, err := lockRecord()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := readRecordControl()
	if err != nil {
		return err
	}
	if current == nil || current.Token != c.Token {
		return nil
	}
	removeRecordTemp(c)
	return os.Remove(recordPath())
}

func requestRecorder(c *recordControl, method, path string, timeout time.Duration) (recordResponse, error) {
	var out recordResponse
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d/%s", c.Port, path), nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("X-Rodney-Token", c.Token)
	client := http.Client{Timeout: timeout}
	res, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return out, fmt.Errorf("recorder returned HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// recordingActive also clears metadata left by a dead worker. A fresh
// metadata file without a port belongs to a start still in progress.
func recordingActiveLocked() (bool, error) {
	c, err := readRecordControl()
	if err != nil {
		if info, statErr := os.Stat(recordPath()); statErr == nil && time.Since(info.ModTime()) < 15*time.Second {
			return true, nil
		}
		os.Remove(recordPath())
		return false, nil
	}
	if c == nil {
		return false, nil
	}
	if c.Error != "" {
		removeRecordTemp(c)
		os.Remove(recordPath())
		return false, nil
	}
	if c.Port == 0 {
		if time.Since(c.Created) < 15*time.Second {
			return true, nil
		}
		removeRecordTemp(c)
		os.Remove(recordPath())
		return false, nil
	}
	for i := 0; i < 3; i++ {
		if _, err := requestRecorder(c, http.MethodGet, "status", time.Second); err == nil {
			return true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	removeRecordTemp(c)
	os.Remove(recordPath())
	return false, nil
}

func lockRecordingMutation() (func(), error) {
	unlock, err := lockRecord()
	if err != nil {
		return nil, err
	}
	active, err := recordingActiveLocked()
	if err != nil {
		unlock()
		return nil, err
	}
	if active {
		unlock()
		return nil, fmt.Errorf("cannot change injected scripts during a recording")
	}
	return unlock, nil
}

func checkRecordOutput(name string) (string, error) {
	if filepath.Ext(name) != ".mp4" {
		return "", fmt.Errorf("recording output must have a .mp4 extension")
	}
	path, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("output already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("output directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("output parent is not a directory")
	}
	return path, nil
}

func checkRecordEncoder() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg is required for recording: %w", err)
	}
	out, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").CombinedOutput()
	if err != nil {
		return fmt.Errorf("inspect ffmpeg encoders: %w: %s", err, out)
	}
	if !bytes.Contains(out, []byte("libx264")) {
		return fmt.Errorf("ffmpeg has no libx264 H.264 encoder")
	}
	return nil
}

func startRecording(name string, fps int) (string, error) {
	if fps < 1 || fps > maxRecordFPS {
		return "", fmt.Errorf("fps must be between 1 and %d", maxRecordFPS)
	}
	output, err := checkRecordOutput(name)
	if err != nil {
		return "", err
	}
	if err := checkRecordEncoder(); err != nil {
		return "", err
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	unlock, err := lockRecord()
	if err != nil {
		return "", err
	}
	active, err := recordingActiveLocked()
	if err != nil {
		unlock()
		return "", err
	}
	if active {
		unlock()
		return "", fmt.Errorf("recording already active")
	}
	s, err := loadState()
	if err != nil {
		unlock()
		return "", err
	}
	browser, err := connectBrowser(s)
	if err != nil {
		unlock()
		return "", err
	}
	page, err := getActivePage(browser, s)
	if err != nil {
		unlock()
		return "", err
	}
	target := page.TargetID
	c := &recordControl{Token: hex.EncodeToString(secret[:]), FPS: fps, Output: output, Created: time.Now()}
	temp, err := os.CreateTemp(filepath.Dir(output), ".rodney-record-*.mp4")
	if err != nil {
		unlock()
		return "", fmt.Errorf("reserve temporary recording: %w", err)
	}
	c.Temp = temp.Name()
	if err := temp.Close(); err != nil {
		unlock()
		os.Remove(c.Temp)
		return "", err
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			removeRecordTemp(c)
		}
	}()
	f, err := os.OpenFile(recordPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		unlock()
		return "", fmt.Errorf("recording already active")
	}
	if err != nil {
		unlock()
		return "", err
	}
	if err := json.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		os.Remove(recordPath())
		unlock()
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(recordPath())
		unlock()
		return "", err
	}
	unlock()
	exe, err := os.Executable()
	if err != nil {
		removeRecordControl(c)
		return "", err
	}
	cmd := exec.Command(exe, "_record", string(target), output)
	cmd.Env = append(os.Environ(), "RODNEY_HOME="+stateDir(), "RODNEY_RECORD_TOKEN="+c.Token)
	if err := cmd.Start(); err != nil {
		removeRecordControl(c)
		return "", fmt.Errorf("start recording worker: %w", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		current, err := readRecordControl()
		if err == nil && current != nil {
			if current.Token != c.Token {
				cmd.Process.Kill()
				cmd.Wait()
				return "", fmt.Errorf("recorder state changed during startup")
			}
			if current.Error != "" {
				cmd.Wait()
				removeRecordControl(c)
				return "", fmt.Errorf("recording worker: %s", current.Error)
			}
			if current.Port > 0 {
				response, err := requestRecorder(current, http.MethodGet, "status", time.Second)
				if err == nil && response.Ready {
					keepTemp = true
					cmd.Process.Release()
					return output, nil
				}
				if err == nil && response.Error != "" {
					cmd.Wait()
					removeRecordControl(c)
					return "", fmt.Errorf("recording worker: %s", response.Error)
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	cmd.Process.Kill()
	cmd.Wait()
	removeRecordControl(c)
	return "", fmt.Errorf("timed out waiting for recorder readiness")
}

func stopRecording() (recordResponse, error) {
	var result recordResponse
	c, err := readRecordControl()
	if err != nil {
		return result, err
	}
	if c == nil {
		return result, fmt.Errorf("no active recording")
	}
	for c.Port == 0 && c.Error == "" && time.Now().Before(c.Created.Add(15*time.Second)) {
		time.Sleep(50 * time.Millisecond)
		current, err := readRecordControl()
		if err != nil || current == nil || current.Token != c.Token {
			return result, fmt.Errorf("%w: recorder state changed during startup", errRecordUnconfirmed)
		}
		c = current
	}
	if c.Error != "" {
		removeRecordControl(c)
		return result, fmt.Errorf("recording failed: %s", c.Error)
	}
	if c.Port == 0 {
		return result, fmt.Errorf("%w: recording is still starting", errRecordUnconfirmed)
	}
	result, err = requestRecorder(c, http.MethodPost, "stop", 2*time.Minute)
	if err != nil {
		return result, fmt.Errorf("%w: recording worker unavailable: %v", errRecordUnconfirmed, err)
	}
	if err := removeRecordControl(c); err != nil {
		return result, err
	}
	if result.Error != "" {
		return result, fmt.Errorf("recording failed: %s", result.Error)
	}
	return result, nil
}

func cmdRecord(args []string) {
	const usage = "usage: rodney record start [--fps N] <file.mp4>\n       rodney record stop"
	if len(args) > 0 && args[0] == "start" {
		flags := flag.NewFlagSet("record start", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		fps := flags.Int("fps", defaultRecordFPS, "output frames per second")
		if err := flags.Parse(args[1:]); err != nil {
			fatal("%v", err)
		}
		if flags.NArg() != 1 {
			fatal("%s", usage)
		}
		output, err := startRecording(flags.Arg(0), *fps)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Println(output)
		return
	}
	if len(args) == 1 && args[0] == "stop" {
		result, err := stopRecording()
		if err != nil {
			fatal("%v", err)
		}
		fmt.Printf("%s (%.2fs)\n", result.Output, result.Duration)
		return
	}
	fatal("%s", usage)
}

func cmdRecordWorker(args []string) {
	if len(args) != 2 || os.Getenv("RODNEY_RECORD_TOKEN") == "" {
		fatal("invalid recorder worker invocation")
	}
	c, err := readRecordControl()
	if err != nil || c == nil || c.Token != os.Getenv("RODNEY_RECORD_TOKEN") {
		fatal("invalid recorder control state")
	}
	if c.FPS < 1 || c.FPS > maxRecordFPS {
		fatal("invalid recorder frame rate")
	}
	s, err := loadState()
	if err != nil {
		fatal("%v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal("start recorder control endpoint: %v", err)
	}
	c.Port, c.PID = listener.Addr().(*net.TCPAddr).Port, os.Getpid()
	if err := writeRecordControl(c); err != nil {
		fatal("write recorder control state: %v", err)
	}
	ready := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	served := make(chan struct{})
	var stopOnce, servedOnce sync.Once
	var isReady atomic.Bool
	var result recordResponse
	go func() {
		select {
		case <-ready:
			isReady.Store(true)
		case <-done:
		}
	}()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-Rodney-Token") != c.Token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		json.NewEncoder(w).Encode(recordResponse{Ready: isReady.Load()})
	})
	mux.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		stopOnce.Do(func() { close(stop) })
		<-done
		json.NewEncoder(w).Encode(result)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		servedOnce.Do(func() { close(served) })
	})
	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	duration, runErr := runRecording(s, proto.TargetTargetID(args[0]), args[1], c.Temp, c.FPS, ready, stop)
	result = recordResponse{Ready: true, Output: c.Output, Duration: duration.Seconds()}
	if runErr != nil {
		result.Error = runErr.Error()
		c.Error = runErr.Error()
		writeRecordControl(c)
	}
	close(done)
	select {
	case <-served:
	case <-time.After(2 * time.Second):
	}
	server.Close()
	if runErr != nil {
		os.Exit(2)
	}
}
