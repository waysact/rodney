package main

import (
	"math"
	"testing"
	"time"
)

func TestMousePathEndpointAndBounds(t *testing.T) {
	vp := Viewport{W: 800, H: 600}
	for _, tc := range []struct{ start, end point }{
		{point{100, 100}, point{700, 300}},
		{point{1, 1}, point{700, 1}},
		{point{799, 599}, point{1, 599}},
		{point{100, 100}, point{105, 105}},
	} {
		for seed := uint64(0); seed < 4; seed++ {
			path := mousePath(tc.start, tc.end, vp, seed)
			if len(path) < 2 || path[0] != tc.start || path[len(path)-1] != tc.end {
				t.Fatalf("seed %d endpoints=%v..%v", seed, path[0], path[len(path)-1])
			}
			for _, p := range path {
				if p.X < 0 || p.Y < 0 || p.X >= 800 || p.Y >= 600 {
					t.Fatalf("seed %d out of bounds: %v", seed, p)
				}
			}
		}
	}
}

func TestMousePathVariesAndProgress(t *testing.T) {
	vp := Viewport{W: 800, H: 600}
	start, end := point{100, 100}, point{700, 300}
	paths := make([][]point, 4)
	for i := range paths {
		paths[i] = mousePath(start, end, vp, uint64(i))
	}
	if paths[0][len(paths[0])/2].Y == paths[1][len(paths[1])/2].Y {
		t.Fatal("left and right bends match")
	}
	if paths[2][len(paths[2])/4].Y == paths[2][3*len(paths[2])/4].Y {
		t.Fatal("S curve has no reversal")
	}
	if p := pointAtDistance(paths[0], math.MaxFloat64); p != end {
		t.Fatalf("end=%v", p)
	}
	if progressAt(0, time.Second) != 0 || progressAt(time.Second, time.Second) != 1 {
		t.Fatal("progress endpoints")
	}
	fast := progressAt(200*time.Millisecond, time.Second) - progressAt(100*time.Millisecond, time.Second)
	slow := progressAt(800*time.Millisecond, time.Second) - progressAt(700*time.Millisecond, time.Second)
	if fast <= slow {
		t.Fatalf("speed did not peak early: fast=%v slow=%v", fast, slow)
	}
}
