package helperkit

import "testing"

func TestParseSinkInputs(t *testing.T) {
	in := []byte(`[{"index":12,"sink":3,"properties":{"application.process.id":"4242","media.name":"x"}},
	               {"index":13,"sink":3,"properties":{"media.name":"no pid"}}]`)
	got, err := ParseSinkInputs(in)
	if err != nil || len(got) != 2 || got[0] != (SinkInput{12, 3, 4242}) || got[1].PID != 0 {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := ParseSinkInputs([]byte("nope")); err == nil {
		t.Error("garbage accepted")
	}
}

func TestParentPID(t *testing.T) {
	for _, tc := range []struct {
		stat string
		ppid int
		ok   bool
	}{
		{"1234 (bash) S 1000 1234 1234 0 -1", 1000, true},
		{"1234 (my (odd) name) S 77 1 1", 77, true},
		{"1234 (cut", 0, false},
		{"1234 (x) S", 0, false},
	} {
		if p, ok := ParentPID(tc.stat); p != tc.ppid || ok != tc.ok {
			t.Errorf("%q: %d %v", tc.stat, p, ok)
		}
	}
}

func TestPickSinkInputs(t *testing.T) {
	// 10 launches 11 launches 12; 20 is unrelated.
	set := Descendants(10, map[int]int{11: 10, 12: 11, 20: 1, 21: 20})
	if !set[10] || !set[11] || !set[12] || set[20] || set[21] {
		t.Fatalf("descendants %v", set)
	}
	got := PickSinkInputs([]SinkInput{{1, 0, 12}, {2, 0, 20}, {3, 0, 0}}, set)
	if len(got) != 1 || got[0].Index != 1 {
		t.Errorf("picked %+v", got)
	}
}
