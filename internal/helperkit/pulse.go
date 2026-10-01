package helperkit

import (
	"encoding/json"
	"strconv"
	"strings"
)

// SinkInput is one playing stream of PulseAudio / PipeWire-pulse, as `pactl -f json list sink-inputs`
// reports it.
type SinkInput struct {
	Index int
	Sink  int // the sink it plays to now
	PID   int // application.process.id, 0 when unknown
}

// ParseSinkInputs reads the JSON of `pactl -f json list sink-inputs`.
func ParseSinkInputs(data []byte) ([]SinkInput, error) {
	var raw []struct {
		Index      int               `json:"index"`
		Sink       int               `json:"sink"`
		Properties map[string]string `json:"properties"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]SinkInput, 0, len(raw))
	for _, r := range raw {
		pid, _ := strconv.Atoi(r.Properties["application.process.id"])
		out = append(out, SinkInput{Index: r.Index, Sink: r.Sink, PID: pid})
	}
	return out, nil
}

// ParentPID reads the parent from the text of /proc/<pid>/stat. The command name sits in
// parentheses and may itself contain spaces and parentheses, so the fields are counted from the last
// closing one.
func ParentPID(stat string) (int, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(stat[i+1:]) // state, ppid, ...
	if len(f) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(f[1])
	return n, err == nil
}

// Descendants is root and every process under it, given each process's parent. A browser or a game
// launcher plays its sound from a child, and the window belongs to the parent.
func Descendants(root int, parent map[int]int) map[int]bool {
	set := map[int]bool{root: true}
	for changed := true; changed; {
		changed = false
		for pid, ppid := range parent {
			if !set[pid] && set[ppid] {
				set[pid] = true
				changed = true
			}
		}
	}
	return set
}

// PickSinkInputs is the streams that belong to one of the processes.
func PickSinkInputs(in []SinkInput, pids map[int]bool) []SinkInput {
	var out []SinkInput
	for _, s := range in {
		if s.PID != 0 && pids[s.PID] {
			out = append(out, s)
		}
	}
	return out
}
