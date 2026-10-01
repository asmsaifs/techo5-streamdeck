package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/helperkit"
)

// sound moves the window's app (and its child processes) onto a private null sink and reads that
// sink's monitor with parec: the app's sound reaches the Show and nothing else, and the app is put
// back where it was on Stop. It works on PulseAudio and on PipeWire's pulse server alike. While the
// capture runs the app is silent on the computer's own speakers; that is the point of a private sink.

const (
	chunkBytes = 20 * 48 * 4 // 20 ms of 48 kHz S16LE stereo
	rescan     = 500 * time.Millisecond
)

type sound struct {
	sink   string
	module string
	parec  *exec.Cmd
	stop   chan struct{}
	wg     sync.WaitGroup

	mu    sync.Mutex
	moved map[int]int // sink-input index -> the sink it came from
}

func pactl(args ...string) ([]byte, error) {
	out, err := exec.Command("pactl", args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("pactl %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("pactl %s: %w", args[0], err)
	}
	return out, nil
}

func startSound(out *helperkit.Out, pid int) (*sound, error) {
	if pid <= 0 {
		return nil, errors.New("the window does not say which process it belongs to (_NET_WM_PID)")
	}
	s := &sound{sink: "deckcap_" + strconv.Itoa(os.Getpid()), stop: make(chan struct{}), moved: map[int]int{}}
	id, err := pactl("load-module", "module-null-sink", "sink_name="+s.sink, "sink_properties=device.description=Deck")
	if err != nil {
		return nil, err
	}
	s.module = strings.TrimSpace(string(id))

	s.parec = exec.Command("parec", "-d", s.sink+".monitor", "--format=s16le", "--rate=48000", "--channels=2", "--latency-msec=20")
	pipe, err := s.parec.StdoutPipe()
	if err == nil {
		err = s.parec.Start()
	}
	if err != nil {
		s.unload()
		return nil, fmt.Errorf("parec: %w", err)
	}
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		buf := make([]byte, chunkBytes)
		for {
			n, err := io.ReadFull(pipe, buf)
			if n > 0 {
				out.Audio(48000, 2, append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer s.wg.Done()
		// Apps open their sound streams late and per track, so the move is repeated.
		t := time.NewTicker(rescan)
		defer t.Stop()
		for {
			s.moveStreams(pid)
			select {
			case <-s.stop:
				return
			case <-t.C:
			}
		}
	}()
	return s, nil
}

func processParents() map[int]int {
	parent := map[int]int{}
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		if pp, ok := helperkit.ParentPID(string(b)); ok {
			parent[pid] = pp
		}
	}
	return parent
}

func (s *sound) moveStreams(pid int) {
	data, err := pactl("-f", "json", "list", "sink-inputs")
	if err != nil {
		return
	}
	all, err := helperkit.ParseSinkInputs(data)
	if err != nil {
		return
	}
	mine := helperkit.PickSinkInputs(all, helperkit.Descendants(pid, processParents()))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, in := range mine {
		if _, done := s.moved[in.Index]; done {
			continue
		}
		if _, err := pactl("move-sink-input", strconv.Itoa(in.Index), s.sink); err == nil {
			s.moved[in.Index] = in.Sink
		}
	}
}

func (s *sound) unload() { pactl("unload-module", s.module) }

// Stop gives the streams back to their sinks and removes the private one.
func (s *sound) Stop() {
	close(s.stop)
	if s.parec.Process != nil {
		s.parec.Process.Kill()
	}
	s.wg.Wait()
	s.parec.Wait()
	s.mu.Lock()
	for idx, sink := range s.moved {
		// A stream that has ended is gone and the move fails; nothing to do about that.
		pactl("move-sink-input", strconv.Itoa(idx), strconv.Itoa(sink))
	}
	s.mu.Unlock()
	s.unload()
}
