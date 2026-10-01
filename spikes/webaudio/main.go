// Command webaudio is spike 0.5.2 option A: can a headless Chrome tab capture its own sound with
// getDisplayMedia, with no one there to accept the prompt? It serves a page that plays a tone,
// clicks it (getDisplayMedia wants a user gesture), asks for the tab with audio, and measures the
// captured audio's level in an AudioWorklet. A level well above zero means the tab's sound can be
// had as PCM inside the page, and from there sent to the Go core.
//
//	go run ./spikes/webaudio -chrome "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const page = `<!doctype html><html><body style="background:#123;color:#fff;font:20px sans-serif">
<button id=go style="width:100vw;height:100vh">capture</button>
<script>
window.result = {state: "waiting for the click"};
document.getElementById("go").onclick = async () => {
  try {
    // The page's own sound: a tone that keeps playing.
    const play = new AudioContext();
    const osc = play.createOscillator();
    osc.frequency.value = 440;
    osc.connect(play.destination);
    osc.start();

    const stream = await navigator.mediaDevices.getDisplayMedia({
      video: true, preferCurrentTab: true, selfBrowserSurface: "include",
      // Music, not a call: no processing, both channels. suppressLocalAudioPlayback keeps the tab's
      // sound off this computer's speakers while it is captured: it goes to the Show instead.
      audio: {echoCancellation: false, autoGainControl: false, noiseSuppression: false,
              channelCount: 2, suppressLocalAudioPlayback: true},
    });
    const tracks = stream.getAudioTracks();
    window.result = {state: "captured", audioTracks: tracks.length,
      label: tracks[0] ? tracks[0].label : "", settings: tracks[0] ? tracks[0].getSettings() : null};
    if (!tracks.length) return;

    const rec = new AudioContext({sampleRate: 48000});
    const code = "class P extends AudioWorkletProcessor{process(i){const c=i[0][0];if(c){let s=0;for(const v of c)s+=v*v;this.port.postMessage(Math.sqrt(s/c.length));}return true}}registerProcessor('p',P)";
    await rec.audioWorklet.addModule(URL.createObjectURL(new Blob([code], {type: "text/javascript"})));
    const node = new AudioWorkletNode(rec, "p");
    let peak = 0, blocks = 0;
    node.port.onmessage = e => { blocks++; peak = Math.max(peak, e.data);
      window.result = Object.assign({}, window.result, {blocks, peakRMS: peak, rate: rec.sampleRate}); };
    rec.createMediaStreamSource(new MediaStream(tracks)).connect(node);
  } catch (e) {
    window.result = {state: "failed", error: String(e)};
  }
};
</script></body></html>`

func main() {
	chrome := flag.String("chrome", "", "the browser to run (default: chromedp's search)")
	headless := flag.String("headless", "new", "new, old, or off")
	fakeUI := flag.Bool("fakeui", false, "also pass --use-fake-ui-for-media-stream")
	flag.Parse()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, page)
	}))

	opts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("auto-accept-this-tab-capture", true),
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
		chromedp.WindowSize(960, 480),
	}
	switch *headless {
	case "new":
		opts = append(opts, chromedp.Flag("headless", "new"))
	case "old":
		opts = append(opts, chromedp.Flag("headless", true))
	}
	if *fakeUI {
		opts = append(opts, chromedp.Flag("use-fake-ui-for-media-stream", true))
	}
	if *chrome != "" {
		opts = append(opts, chromedp.ExecPath(*chrome))
	}
	actx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()
	ctx, cancel := chromedp.NewContext(actx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var result map[string]any
	err = chromedp.Run(ctx,
		chromedp.Navigate("http://"+ln.Addr().String()+"/"),
		chromedp.WaitVisible("#go"),
		// No click on the page: a real web tile cannot click somewhere for it. Script run with
		// CDP's userGesture is a gesture as far as the page is concerned.
		chromedp.Evaluate(`document.getElementById("go").click()`, nil,
			func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithUserGesture(true) }),
		chromedp.Sleep(3*time.Second),
		chromedp.Evaluate(`window.result`, &result),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "webaudio:", err)
		os.Exit(1)
	}
	fmt.Printf("headless=%s fakeui=%v: %v\n", *headless, *fakeUI, result)
}
