package main

import (
	"math"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

type point struct{ X, Y float64 }

func mousePath(start, end point, vp Viewport, seed uint64) []point {
	const segments = 80
	path := make([]point, segments+1)
	dx, dy := end.X-start.X, end.Y-start.Y
	distance := math.Hypot(dx, dy)
	amplitude := math.Min(distance*.09, 55)
	if distance < 25 {
		amplitude = 0
	}
	if seed%2 == 1 {
		amplitude = -amplitude
	}
	nx, ny := 0.0, 0.0
	if distance > 0 {
		nx, ny = -dy/distance, dx/distance
	}
	for {
		control1 := point{start.X + dx*.28 + nx*amplitude, start.Y + dy*.28 + ny*amplitude}
		control2 := point{start.X + dx*.72 + nx*amplitude, start.Y + dy*.72 + ny*amplitude}
		if seed%4 >= 2 {
			control2 = point{start.X + dx*.72 - nx*amplitude, start.Y + dy*.72 - ny*amplitude}
		}
		inside := true
		for i := 0; i <= segments; i++ {
			t := float64(i) / segments
			u := 1 - t
			path[i] = point{
				X: u*u*u*start.X + 3*u*u*t*control1.X + 3*u*t*t*control2.X + t*t*t*end.X,
				Y: u*u*u*start.Y + 3*u*u*t*control1.Y + 3*u*t*t*control2.Y + t*t*t*end.Y,
			}
			if path[i].X < 0 || path[i].Y < 0 || path[i].X >= float64(vp.W) || path[i].Y >= float64(vp.H) {
				inside = false
			}
		}
		path[0], path[segments] = start, end
		if inside || math.Abs(amplitude) < .001 {
			break
		}
		amplitude *= .5
	}
	return path
}

func pointAtDistance(path []point, distance float64) point {
	if len(path) == 0 {
		return point{}
	}
	if distance <= 0 {
		return path[0]
	}
	for i := 1; i < len(path); i++ {
		segment := math.Hypot(path[i].X-path[i-1].X, path[i].Y-path[i-1].Y)
		if distance <= segment {
			f := 0.0
			if segment > 0 {
				f = distance / segment
			}
			return point{path[i-1].X + (path[i].X-path[i-1].X)*f, path[i-1].Y + (path[i].Y-path[i-1].Y)*f}
		}
		distance -= segment
	}
	return path[len(path)-1]
}

func progressAt(elapsed, duration time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	if duration <= 0 || elapsed >= duration {
		return 1
	}
	t := float64(elapsed) / float64(duration)
	const peak = .2
	if t <= peak {
		return t * t / peak
	}
	return 1 - (1-t)*(1-t)/(1-peak)
}

func mouseButtonMask(button proto.InputMouseButton) int {
	switch button {
	case proto.InputMouseButtonLeft:
		return 1
	case proto.InputMouseButtonRight:
		return 2
	case proto.InputMouseButtonMiddle:
		return 4
	default:
		return 0
	}
}

func runMouseMotion(send mouseSender, start, end point, vp Viewport, duration time.Duration, button proto.InputMouseButton, onConfirmed func(point) error) error {
	path := mousePath(start, end, vp, uint64(time.Now().UnixNano()))
	total := 0.0
	for i := 1; i < len(path); i++ {
		total += math.Hypot(path[i].X-path[i-1].X, path[i].Y-path[i-1].Y)
	}
	steps := int(duration / (time.Second / 60))
	if steps < 2 {
		steps = 2
	}
	if steps > 180 {
		steps = 180
	}
	started := time.Now()
	for i := 1; i <= steps; i++ {
		elapsed := time.Duration(float64(duration) * float64(i) / float64(steps))
		if wait := time.Until(started.Add(elapsed)); wait > 0 {
			time.Sleep(wait)
		}
		position := pointAtDistance(path, total*progressAt(elapsed, duration))
		if i == steps {
			position = end
		}
		mask := mouseButtonMask(button)
		event := proto.InputDispatchMouseEvent{Type: proto.InputDispatchMouseEventTypeMouseMoved, X: position.X, Y: position.Y, Button: button, Buttons: &mask}
		if err := send(event); err != nil {
			return err
		}
		if onConfirmed != nil {
			if err := onConfirmed(position); err != nil {
				return err
			}
		}
	}
	return nil
}
