//go:build !windows

// Command sendspin is spike 10.1: does a Sendspin sender written in Go play on the Show? It finds
// the Show by mDNS, plays a sine through internal/sendspin at the given lead for the given time,
// paced like a sound device (20 ms at a time), then goes silent until the Group lets the Show go.
// It prints how long the Show took to connect, what was sent and dropped, and the CPU the run
// cost. Whether it played without clicks is for the ears, and for the Show's log:
// "sendspin ahead ... late=0 dropped=0" in /data/techo5-linux/techo5.log.
//
//	go run ./spikes/sendspin -show "Echo Show 5 2nd gen" -lead 200ms -for 10m
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"syscall"
	"time"

	"github.com/asmsaifs/techo5-streamdeck/internal/audio"
	"github.com/asmsaifs/techo5-streamdeck/internal/sendspin"
)

func main() {
	show := flag.String("show", "", "the Show's name as it advertises itself (empty: list what answers)")
	lead := flag.Duration("lead", sendspin.DefaultLead, "how far ahead chunks are stamped")
	dur := flag.Duration("for", 10*time.Second, "how long the tone plays")
	freq := flag.Float64("freq", 440, "tone frequency (Hz)")
	db := flag.Float64("db", -20, "tone level (dBFS)")
	flag.Parse()

	f := &sendspin.Finder{}
	if *show == "" {
		list, err := f.Look(context.Background(), 3*time.Second)
		if err != nil {
			fail(err)
		}
		for _, p := range list {
			fmt.Printf("%-30s %s\n", p.Name, p.URL)
		}
		return
	}

	var connectedAt time.Time
	g := sendspin.NewGroup([]string{*show}, sendspin.Options{
		Name: "techo5-streamdeck spike", ID: "spike", Lead: *lead,
		Resolve: f.Resolve, Forget: f.Forget,
		Changed: func() {},
	})
	defer g.Close()

	start := time.Now()
	cpu0 := cpuTime()
	amp := 32767 * math.Pow(10, *db/20)
	pcm := make([]byte, audio.ChunkFrames*4)
	n := 0
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	report := time.Now()
	lastSaid := fmt.Sprint([]string(nil), map[string]string{})
	for time.Since(start) < *dur {
		<-tick.C
		for i := range audio.ChunkFrames {
			v := uint16(int16(amp * math.Sin(2*math.Pi**freq*float64(n)/audio.Rate)))
			binary.LittleEndian.PutUint16(pcm[i*4:], v)
			binary.LittleEndian.PutUint16(pcm[i*4+2:], v)
			n++
		}
		g.Write(audio.Wire, pcm)
		st := g.State()
		if connectedAt.IsZero() && len(st.Playing) > 0 {
			connectedAt = time.Now()
			fmt.Printf("connected %v after the first sound (100 ms of it is the gate)\n", connectedAt.Sub(start).Round(time.Millisecond))
		}
		if said := fmt.Sprint(st.Busy, st.Failed); said != lastSaid {
			lastSaid = said
			fmt.Printf("busy %v failed %v\n", st.Busy, st.Failed)
		}
		if time.Since(report) >= 10*time.Second {
			report = time.Now()
			fmt.Printf("%v: playing on %v\n", time.Since(start).Round(time.Second), st.Playing)
		}
	}
	used := cpuTime() - cpu0
	fmt.Printf("tone done: %v of sound, %v CPU (%.2f%% of one core)\n",
		time.Since(start).Round(time.Millisecond), used.Round(time.Millisecond), 100*used.Seconds()/time.Since(start).Seconds())

	// Silence until the Group lets the Show go, as a sound device left on would deliver it.
	silence := make([]byte, audio.ChunkFrames*4)
	quiet := time.Now()
	for g.State().Open {
		<-tick.C
		g.Write(audio.Wire, silence)
	}
	fmt.Printf("the Show was let go %v after the sound stopped\n", time.Since(quiet).Round(time.Millisecond))
}

func cpuTime() time.Duration {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sendspin:", err)
	os.Exit(1)
}
