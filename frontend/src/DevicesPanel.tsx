import { useCallback, useEffect, useRef, useState } from "react";
import type { Device, Settings } from "./api";
import { devices, errorText, regenerateKey, setListen, settings } from "./api";
import { secretStatus, setSecret } from "./api";
import type { SecretStatus } from "./api";
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

        <Integrations cfg={p.cfg} edit={p.edit} say={p.say} />
      </div>
    </div>
  );
}

/** The addresses of Home Assistant and OBS (part of the config, saved with it), and their token
 * and password (kept in the system keychain at once, and never shown again). */
function Integrations(p: { cfg: m.Config; edit: (c: m.Config) => void; say: Say }) {
  const [st, setSt] = useState<SecretStatus | null>(null);
  const [token, setToken] = useState("");
  const [pw, setPw] = useState("");
  const load = useCallback(() => void secretStatus().then(setSt).catch(() => {}), []);
  useEffect(load, [load]);
  const keep = (which: "homeassistant" | "obs", value: string, clear: () => void) =>
    setSecret(which, value)
      .then(() => (clear(), load(), p.say({ kind: "ok", text: value ? "Kept in the system keychain." : "Removed from the keychain." })))
      .catch((e) => p.say({ kind: "err", text: errorText(e) }));
  const inn = p.cfg.integrations ?? {};

  return (
    <>
      <h3>Integrations</h3>
      <label>
        Home Assistant address
        <input spellCheck={false} placeholder="http://homeassistant.local:8123" value={inn.homeassistant ?? ""} onChange={(e) => p.edit(m.setIntegration(p.cfg, "homeassistant", e.target.value))} />
      </label>
      <label>
        Long-lived access token {st?.HomeAssistantToken && <small className="hint">(one is kept)</small>}
        <div className="row">
          <input style={{ flex: 1 }} type="password" autoComplete="off" value={token} placeholder={st?.HomeAssistantToken ? "enter a new one to replace it" : "from your Home Assistant profile"} onChange={(e) => setToken(e.target.value)} />
          <button disabled={!token} onClick={() => keep("homeassistant", token, () => setToken(""))}>Keep</button>
          <button disabled={!st?.HomeAssistantToken} onClick={() => keep("homeassistant", "", () => setToken(""))}>Remove</button>
        </div>
      </label>
      <label>
        OBS address
        <input spellCheck={false} placeholder="localhost:4455" value={inn.obs ?? ""} onChange={(e) => p.edit(m.setIntegration(p.cfg, "obs", e.target.value))} />
      </label>
      <label>
        OBS password {st?.OBSPassword && <small className="hint">(one is kept)</small>}
        <div className="row">
          <input style={{ flex: 1 }} type="password" autoComplete="off" value={pw} placeholder="empty if OBS has none" onChange={(e) => setPw(e.target.value)} />
          <button disabled={!pw} onClick={() => keep("obs", pw, () => setPw(""))}>Keep</button>
          <button disabled={!st?.OBSPassword} onClick={() => keep("obs", "", () => setPw(""))}>Remove</button>
        </div>
      </label>
      <p className="hint">Addresses are saved with the deck. Tokens and passwords go to the system keychain as soon as you press Keep, and are not in config.json.</p>
    </>
  );
}
