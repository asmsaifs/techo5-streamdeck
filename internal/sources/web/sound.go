package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Sound modes of a tile (Spec.Sound).
const (
	SoundShow    = "show"    // the page's sound goes to the Show, and not out of the computer
	SoundDesktop = "desktop" // the page's sound comes out of the computer as usual
	SoundOff     = "off"     // nowhere
)

// captureMax bounds one message from the page: it sends 20 ms at a time, about 5 KB, so more than
// this is not our script talking.
const captureMax = 1 << 20

// captureScript is injected into every document of a tile's tab. It defines window.__deckAudio and
// does nothing until start() is called, which needs a user gesture: Runtime.evaluate with
// userGesture counts as one (docs/spikes.md, spike 0.5.2).
//
// start() asks for this tab with its audio (Chrome is run with --auto-accept-this-tab-capture, so
// no one has to accept), keeps the sound off the computer's speakers while it is captured, and
// sends it out in 20 ms blocks of S16LE 48 kHz stereo through the binding, base64 encoded. The
// AudioContext runs at 48 kHz and resamples whatever the track is, so no resampling is needed on
// our side. Processing that suits a call (echo cancellation, gain, noise suppression) is off, and
// both channels are asked for.
const captureScript = `(() => {
  if (window.__deckAudio) return;
  const bind = %q;
  const worklet = "class P extends AudioWorkletProcessor{constructor(){super();this.b=new Int16Array(1920);this.n=0}" +
    "process(i){const c=i[0];if(!c||!c[0])return true;const l=c[0],r=c[1]||c[0];" +
    "for(let k=0;k<l.length;k++){const a=l[k],b=r[k];this.b[this.n++]=(a<-1?-1:a>1?1:a)*32767;this.b[this.n++]=(b<-1?-1:b>1?1:b)*32767;" +
    "if(this.n===1920){this.port.postMessage(this.b.buffer.slice(0));this.n=0}}return true}}registerProcessor('deck',P)";
  let running = null, stop = () => {};
  async function begin() {
    const stream = await navigator.mediaDevices.getDisplayMedia({
      video: true, preferCurrentTab: true, selfBrowserSurface: "include",
      audio: {echoCancellation: false, autoGainControl: false, noiseSuppression: false,
              channelCount: 2, suppressLocalAudioPlayback: true},
    });
    const tracks = stream.getAudioTracks();
    if (!tracks.length) { stream.getTracks().forEach(t => t.stop()); throw new Error("the tab has no audio track"); }
    // Only the sound is wanted. The video track would also compete with the screencast for the
    // page's frames, which then stop.
    stream.getVideoTracks().forEach(t => t.stop());
    const ctx = new AudioContext({sampleRate: 48000});
    await ctx.audioWorklet.addModule(URL.createObjectURL(new Blob([worklet], {type: "text/javascript"})));
    const node = new AudioWorkletNode(ctx, "deck");
    node.port.onmessage = e => {
      const u = new Uint8Array(e.data); let s = "";
      for (let i = 0; i < u.length; i += 8192) s += String.fromCharCode.apply(null, u.subarray(i, i + 8192));
      window[bind](btoa(s));
    };
    ctx.createMediaStreamSource(new MediaStream(tracks)).connect(node);
    stop = () => { stream.getTracks().forEach(t => t.stop()); ctx.close(); running = null; stop = () => {}; };
    tracks[0].onended = () => stop();
  }
  window.__deckAudio = {
    start() { return running = running || begin().then(() => "ok", e => { running = null; throw e; }); },
    stop() { stop(); return "stopped"; },
  };
})()`

func newBinding() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "deck_" + hex.EncodeToString(b[:])
}

func captureSource(binding string) string { return fmt.Sprintf(captureScript, binding) }

// userGesture makes Runtime.evaluate count as a click.
func userGesture(p *runtime.EvaluateParams) *runtime.EvaluateParams {
	return p.WithUserGesture(true).WithAwaitPromise(true)
}

// startCapture begins capturing the page's sound, if it is not already. It is called when a source
// that wants the sound has the tab, and again after each document load, because a full navigation
// ends the capture with the page that was capturing.
func (t *tab) startCapture(ctx context.Context) error {
	var res string
	err := chromedp.Run(ctx, chromedp.Evaluate(`window.__deckAudio ? window.__deckAudio.start() : "not ready"`, &res, userGesture))
	if err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("the page's sound could not be captured: %s", res)
	}
	return nil
}

// stopCapture ends the capture; the tab is being parked or closed.
func (t *tab) stopCapture(ctx context.Context) {
	_ = chromedp.Run(ctx, chromedp.Evaluate(`window.__deckAudio && window.__deckAudio.stop()`, nil))
}

// onSound passes a block of sound from the page on to the source that has the tab.
func (t *tab) onSound(e *runtime.EventBindingCalled) {
	if e.Name != t.binding || len(e.Payload) > captureMax {
		return
	}
	s := t.current()
	if s == nil {
		return
	}
	pcm, err := base64.StdEncoding.DecodeString(strings.TrimSpace(e.Payload))
	if err != nil {
		return
	}
	s.sound(pcm)
}
