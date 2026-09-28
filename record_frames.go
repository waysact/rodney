package main

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"time"
)

const (
	defaultRecordFPS = 15
	maxRecordFPS     = 30
)

// framePacer turns sporadic screencast frames into a constant-rate stream.
// It keeps only the most recent JPEG, so a slow encoder cannot grow a queue.
type framePacer struct {
	write  func([]byte) error
	fps    int
	start  time.Time
	latest []byte
	width  int
	height int
	next   int64
}

// frameQueue gives the encoder a fixed amount of work. A full queue is a
// recording error: silently dropping output frames would shorten the video.
type frameQueue struct{ frames chan []byte }

func newFrameQueue(capacity int) *frameQueue { return &frameQueue{frames: make(chan []byte, capacity)} }

func (q *frameQueue) offer(data []byte) error {
	select {
	case q.frames <- bytes.Clone(data):
		return nil
	default:
		return fmt.Errorf("encoder too slow: frame queue is full")
	}
}

func newFramePacer(write func([]byte) error, fps int) *framePacer {
	return &framePacer{write: write, fps: fps}
}

func (p *framePacer) offer(data []byte, at time.Time) error {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid screencast JPEG: %w", err)
	}
	if p.latest != nil && (cfg.Width != p.width || cfg.Height != p.height) {
		return fmt.Errorf("recorded frame dimensions changed from %dx%d to %dx%d", p.width, p.height, cfg.Width, cfg.Height)
	}
	if p.latest != nil {
		if err := p.flush(at); err != nil {
			return err
		}
	} else {
		p.start, p.width, p.height = at, cfg.Width, cfg.Height
	}
	p.latest = append(p.latest[:0], data...)
	return nil
}

func (p *framePacer) flush(until time.Time) error {
	if p.latest == nil {
		return nil
	}
	for p.start.Add(time.Duration(p.next) * time.Second / time.Duration(p.fps)).Before(until) {
		if err := p.write(p.latest); err != nil {
			return fmt.Errorf("write recording frame: %w", err)
		}
		p.next++
	}
	return nil
}

func (p *framePacer) duration() time.Duration {
	return time.Duration(p.next) * time.Second / time.Duration(p.fps)
}
