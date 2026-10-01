package main

import (
	"reflect"
	"testing"

	"github.com/asmsaifs/techo5-streamdeck/internal/wire"
)

func TestGestures(t *testing.T) {
	type step struct {
		op   string // press, move, release
		x, y int
	}
	tests := []struct {
		name  string
		steps []step
		want  []wire.Touch
		leave bool
	}{
		{"a tap is sent on release, at where it landed",
			[]step{{"press", 500, 200}, {"move", 505, 208}, {"release", 506, 210}},
			[]wire.Touch{{T: "tap", X: 500, Y: 200}}, false},
		{"a drag is down, moves and up",
			[]step{{"press", 500, 200}, {"move", 500, 230}, {"move", 500, 260}, {"release", 500, 270}},
			[]wire.Touch{{T: "down", X: 500, Y: 200}, {T: "move", X: 500, Y: 230}, {T: "move", X: 500, Y: 260},
				{T: "move", X: 500, Y: 270}, {T: "up", X: 500, Y: 270}}, false},
		{"a quick flick with no move event between is still a drag",
			[]step{{"press", 500, 200}, {"release", 500, 300}},
			[]wire.Touch{{T: "down", X: 500, Y: 200}, {T: "move", X: 500, Y: 300}, {T: "up", X: 500, Y: 300}}, false},
		{"a tap in the left strip is still a tap",
			[]step{{"press", 10, 200}, {"release", 10, 200}},
			[]wire.Touch{{T: "tap", X: 10, Y: 200}}, false},
		{"a drag from the left edge leaves the stream",
			[]step{{"press", 10, 200}, {"move", 60, 200}, {"release", 200, 200}},
			nil, true},
		{"a short drag from the left edge does nothing",
			[]step{{"press", 10, 200}, {"release", 60, 200}},
			nil, false},
		{"a drag from the top is the device's",
			[]step{{"press", 500, 10}, {"move", 500, 100}, {"release", 500, 200}},
			nil, false},
		{"a drag from the right edge is the device's",
			[]step{{"press", 950, 200}, {"release", 700, 200}},
			nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &gestures{w: 960, h: 480}
			var got []wire.Touch
			var leave bool
			for _, s := range tt.steps {
				switch s.op {
				case "press":
					g.press(s.x, s.y)
				case "move":
					got = append(got, g.move(s.x, s.y)...)
				case "release":
					out, l := g.release(s.x, s.y)
					got, leave = append(got, out...), l
				}
			}
			if !reflect.DeepEqual(got, tt.want) || leave != tt.leave {
				t.Errorf("got %v leave %v, want %v leave %v", got, leave, tt.want, tt.leave)
			}
		})
	}
}
