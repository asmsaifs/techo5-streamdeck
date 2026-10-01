import { useCallback, useEffect, useRef, useState } from "react";
import type { Device, Settings } from "./api";
import { devices, errorText, regenerateKey, setListen, settings } from "./api";
import * as m from "./model";

type Say = (x: { kind: "err" | "ok"; text: string } | null) => void;

/** Shows that are connected or known, each with the profile it gets; and the server's address and key. */
export function DevicesPanel(p: { cfg: m.Config; edit: (c: m.Config) => void; say: Say; onClose: () => void }) {
  const [live, setLive] = useState<Device[]>([]);
  const [rates, setRates] = useState<Record<string, { kbps: number; fps: number }>>({});
  const prev = useRef<Record<string, { t: number; bytes: number; frames: number }>>({});
  const [set, setSet] = useState<Settings | null>(null);
  const [listen, setListenText] = useState("");
  const [showKey, setShowKey] = useState(false);

  const loadSettings = useCallback(() => {
    settings()
      .then((s) => (setSet(s), setListenText(s.Listen)))
      .catch((e) => p.say({ kind: "err", text: errorText(e) }));
  }, [p]);
  useEffect(loadSettings, []); // eslint-disable-line react-hooks/exhaustive-deps

  // The list is read every couple of seconds; the rates come from the change between two reads.
  useEffect(() => {
    let stop = false;
    const tick = () =>
      devices()
        .then((list) => {
          if (stop) return;
          const t = Date.now();
          const r: Record<string, { kbps: number; fps: number }> = {};
          for (const d of list) {
            const now = { t, bytes: d.Bytes, frames: d.Frames };
            r[d.Name] = m.rate(prev.current[d.Name], now);
            prev.current[d.Name] = now;
          }
          setLive(list ?? []);
          setRates(r);
        })
        .catch(() => {});
    tick();
    const id = setInterval(tick, 2000);
    return () => ((stop = true), clearInterval(id));
  }, []);

  const known = Object.keys(p.cfg.devices ?? {});
  const names = [...new Set([...live.map((d) => d.Name), ...known])];
  const profiles = Object.keys(p.cfg.profiles);
  const instructions = set
    ? `On the Show: Settings → Dashboard server = ${set.Address}\nKey = ${set.Key}\nDashboard = Streamed, then swipe in from the left edge.`
    : "";

  return (
    <div className="modal" onClick={p.onClose}>
      <div className="dialog" onClick={(e) => e.stopPropagation()}>
        <div className="row">
          <h3 style={{ margin: 0 }}>Shows</h3>
          <span className="spacer" />
          <button onClick={p.onClose}>Close</button>
        </div>
        {names.length === 0 && <p className="hint">No Show has connected yet. Follow the steps below on the Show.</p>}
        {names.length > 0 && (
          <table>
            <thead>
              <tr>
                <th>Show</th>
                <th>Status</th>
                <th>Profile</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {names.map((n) => {
                const d = live.find((x) => x.Name === n);
                const r = rates[n];
                const profile = p.cfg.devices?.[n]?.profile ?? "default";
                return (
                  <tr key={n}>
                    <td>{n || "(no name)"}</td>
                    <td className="dim">
                      {d ? (
                        <>
                          <span className="dot on" /> {d.Addr.replace(/:\d+$/, "")} · {d.W}×{d.H} · {d.Source} · {r ? `${r.fps.toFixed(0)} pic/s, ${r.kbps.toFixed(0)} kbit/s` : ""}
                        </>
                      ) : (
                        <>
                          <span className="dot" /> offline
                        </>
                      )}
                    </td>
                    <td>
                      <select value={profile} onChange={(e) => p.edit(m.setDeviceProfile(p.cfg, n, e.target.value))}>
                        {profiles.map((x) => (
                          <option key={x}>{x}</option>
                        ))}
                      </select>
                    </td>
                    <td>{!d && known.includes(n) && <button onClick={() => p.edit(m.forgetDevice(p.cfg, n))}>Forget</button>}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        <p className="hint">A Show that is not listed gets the “default” profile. Profile changes apply once you save.</p>

        <h3>Server</h3>
        {set && (
          <>
            <p className={set.Running ? "" : "msg err"} style={{ cursor: "default" }}>
              {set.Running ? "The deck is serving." : "The deck is paused or could not start. Check the address, or use Resume in the tray menu."}
            </p>
            <label>
              Listen address
              <div className="row">
                <input style={{ flex: 1 }} value={listen} onChange={(e) => setListenText(e.target.value)} spellCheck={false} />
                <button
                  disabled={listen === set.Listen}
                  onClick={() =>
                    setListen(listen.trim())
                      .then(() => (p.say({ kind: "ok", text: "The deck moved to " + listen.trim() }), loadSettings()))
                      .catch((e) => p.say({ kind: "err", text: errorText(e) }))
                  }
                >
                  Apply
                </button>
              </div>
              <small className="hint">0.0.0.0 is every network this computer is on. A LAN address keeps the deck off the others.</small>
            </label>
            <label>
              Key
              <div className="row">
                <input style={{ flex: 1 }} readOnly value={showKey ? set.Key : "•".repeat(16)} />
                <button onClick={() => setShowKey(!showKey)}>{showKey ? "Hide" : "Show"}</button>
                <button
                  onClick={() => {
                    if (!window.confirm("Make a new key? Every Show will be disconnected until it is given the new one.")) return;
                    regenerateKey()
                      .then(() => (p.say({ kind: "ok", text: "New key made. Enter it on each Show." }), loadSettings()))
                      .catch((e) => p.say({ kind: "err", text: errorText(e) }));
                  }}
                >
                  New key
                </button>
              </div>
            </label>
            <h3>Setting up a Show</h3>
            <pre className="cmd">{showKey ? instructions : instructions.replace(set.Key, "•".repeat(16) + " (press Show above)")}</pre>
            <button onClick={() => navigator.clipboard.writeText(instructions).then(() => p.say({ kind: "ok", text: "Copied, with the key." }))}>
              Copy instructions
            </button>
          </>
        )}
      </div>
    </div>
  );
}
