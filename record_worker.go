package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type recordingEncoder struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stderr      bytes.Buffer
	temp        string
	output      string
	queue       *frameQueue
	queueClosed bool
	writerDone  chan struct{}
	writerErr   error
}

func newRecordingEncoder(output string) (*recordingEncoder, error) {
	return newRecordingEncoderAt(output, "", defaultRecordFPS)
}

func newRecordingEncoderAt(output, temp string, fps int) (*recordingEncoder, error) {
	if temp == "" {
		f, err := os.CreateTemp(filepath.Dir(output), ".rodney-record-*.mp4")
		if err != nil {
			return nil, fmt.Errorf("create temporary recording: %w", err)
		}
		temp = f.Name()
		if err := f.Close(); err != nil {
			os.Remove(temp)
			return nil, err
		}
	}
	e := &recordingEncoder{temp: temp, output: output}
	e.cmd = exec.Command("ffmpeg", "-y", "-nostdin", "-loglevel", "error", "-f", "image2pipe", "-vcodec", "mjpeg", "-framerate", strconv.Itoa(fps), "-i", "pipe:0", "-an", "-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2,scale=in_range=full:out_range=limited,format=yuv420p", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-color_range", "tv", "-movflags", "+faststart", "-f", "mp4", temp)
	e.cmd.Stderr = &e.stderr
	stdin, err := e.cmd.StdinPipe()
	if err != nil {
		os.Remove(temp)
		return nil, err
	}
	e.stdin = stdin
	if err := e.cmd.Start(); err != nil {
		os.Remove(temp)
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	e.queue = newFrameQueue(2 * fps)
	e.writerDone = make(chan struct{})
	go func() {
		defer close(e.writerDone)
		for frame := range e.queue.frames {
			if _, err := e.stdin.Write(frame); err != nil {
				e.writerErr = err
				return
			}
		}
		e.writerErr = e.stdin.Close()
	}()
	return e, nil
}

func (e *recordingEncoder) write(frame []byte) error {
	select {
	case <-e.writerDone:
		if e.writerErr == nil {
			return fmt.Errorf("ffmpeg input closed")
		}
		return fmt.Errorf("ffmpeg input failed: %w", e.writerErr)
	default:
	}
	return e.queue.offer(frame)
}

func (e *recordingEncoder) closeQueue() {
	if !e.queueClosed {
		close(e.queue.frames)
		e.queueClosed = true
	}
}

func (e *recordingEncoder) finish() error {
	e.closeQueue()
	select {
	case <-e.writerDone:
	case <-time.After(30 * time.Second):
		e.cmd.Process.Kill()
		<-e.writerDone
		e.cmd.Wait()
		os.Remove(e.temp)
		return fmt.Errorf("timed out waiting for ffmpeg input")
	}
	if e.writerErr != nil {
		e.cmd.Wait()
		os.Remove(e.temp)
		return fmt.Errorf("ffmpeg input failed: %w: %s", e.writerErr, e.stderr.String())
	}
	wait := make(chan error, 1)
	go func() { wait <- e.cmd.Wait() }()
	var processErr error
	select {
	case processErr = <-wait:
	case <-time.After(30 * time.Second):
		e.cmd.Process.Kill()
		<-wait
		os.Remove(e.temp)
		return fmt.Errorf("timed out waiting for ffmpeg to finish")
	}
	if processErr != nil {
		os.Remove(e.temp)
		return fmt.Errorf("ffmpeg failed: %w: %s", processErr, e.stderr.String())
	}
	if err := os.Link(e.temp, e.output); err != nil {
		os.Remove(e.temp)
		return fmt.Errorf("finalize recording: %w", err)
	}
	return os.Remove(e.temp)
}

func (e *recordingEncoder) abort() {
	e.cmd.Process.Kill()
	e.closeQueue()
	<-e.writerDone
	e.cmd.Wait()
	os.Remove(e.temp)
}

type timedRecordFrame struct {
	data []byte
	at   time.Time
}

// runRecording holds its own CDP session for the entire take. The frame
// callback acknowledges promptly and replaces a single pending frame when
// ffmpeg is slower than Chromium.
func runRecording(s *State, target proto.TargetTargetID, output, temp string, fps int, ready chan<- struct{}, stop <-chan struct{}) (time.Duration, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	browser := rod.New().Context(ctx).ControlURL(s.DebugURL).NoDefaultDevice()
	if err := browser.Connect(); err != nil {
		return 0, fmt.Errorf("connect recorder to browser: %w", err)
	}
	page, err := browser.PageFromTarget(target)
	if err != nil {
		return 0, fmt.Errorf("recorded page unavailable: %w", err)
	}
	if err := attachPage(page, s, os.Stderr); err != nil {
		return 0, fmt.Errorf("attach recorded page: %w", err)
	}
	viewport, err := pageViewport(page)
	if err != nil {
		return 0, err
	}
	frames := make(chan timedRecordFrame, 1)
	ackErrors := make(chan error, 1)
	waitEvents := page.EachEvent(func(e *proto.PageScreencastFrame) {
		if err := (proto.PageScreencastFrameAck{SessionID: e.SessionID}).Call(page); err != nil {
			select {
			case ackErrors <- err:
			default:
			}
			return
		}
		f := timedRecordFrame{data: e.Data, at: time.Now()}
		select {
		case frames <- f:
		default:
			select {
			case <-frames:
			default:
			}
			select {
			case frames <- f:
			default:
			}
		}
	})
	go waitEvents()
	if err := (proto.PageStartScreencast{Format: proto.PageStartScreencastFormatJpeg, MaxWidth: &viewport.W, MaxHeight: &viewport.H}).Call(page); err != nil {
		return 0, fmt.Errorf("start screencast: %w", err)
	}
	var first timedRecordFrame
	select {
	case first = <-frames:
	case err := <-ackErrors:
		return 0, fmt.Errorf("acknowledge screencast frame: %w", err)
	case <-time.After(10 * time.Second):
		return 0, fmt.Errorf("timed out waiting for first screencast frame")
	}
	enc, err := newRecordingEncoderAt(output, temp, fps)
	if err != nil {
		return 0, err
	}
	finished := false
	defer func() {
		if !finished {
			enc.abort()
		}
	}()
	pacer := newFramePacer(enc.write, fps)
	if err := pacer.offer(first.data, first.at); err != nil {
		return 0, err
	}
	if pacer.width != viewport.W || pacer.height != viewport.H {
		return 0, fmt.Errorf("screencast frame is %dx%d, viewport is %s", pacer.width, pacer.height, viewport)
	}
	close(ready)
	tick := time.NewTicker(time.Second / time.Duration(fps))
	defer tick.Stop()
	health := time.NewTicker(500 * time.Millisecond)
	defer health.Stop()
	for {
		select {
		case f := <-frames:
			if err := pacer.offer(f.data, f.at); err != nil {
				return 0, err
			}
		case at := <-tick.C:
			if err := pacer.flush(at); err != nil {
				return 0, err
			}
		case <-health.C:
			if _, err := (proto.TargetGetTargetInfo{TargetID: target}).Call(browser.Timeout(2 * time.Second)); err != nil {
				return 0, fmt.Errorf("recorded page or browser closed: %w", err)
			}
		case err := <-ackErrors:
			return 0, fmt.Errorf("acknowledge screencast frame: %w", err)
		case <-stop:
			if err := (proto.PageStopScreencast{}).Call(page); err != nil {
				return 0, fmt.Errorf("stop screencast: %w", err)
			}
			select {
			case f := <-frames:
				if err := pacer.offer(f.data, f.at); err != nil {
					return 0, err
				}
			default:
			}
			if err := pacer.flush(time.Now()); err != nil {
				return 0, err
			}
			if err := enc.finish(); err != nil {
				return 0, err
			}
			finished = true
			return pacer.duration(), nil
		}
	}
}
