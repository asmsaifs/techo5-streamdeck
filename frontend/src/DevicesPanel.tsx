import { useCallback, useEffect, useRef, useState } from "react";
import type { Browser, Device, Settings } from "./api";
import { browsers, devices, errorText, regenerateKey, setListen, settings } from "./api";
import { frontApp, secretStatus, setSecret } from "./api";
import type { SecretStatus, SendspinPlayer, SpeakerStatus } from "./api";
import { speakerShows, speakerStatus, speakerTestTone } from "./api";
import { confirm } from "./Dialog";
import * as m from "./model";

type Say = (x: { kind: "err" | "ok"; text: string } | null) => void;

/** Shows that are connected or known, each with the profile it gets; and the server's address and key. */
export function DevicesPanel(p: { cfg: m.Config; edit: (c: m.Config) => void; say: Say; onClose: () => void }) {
  const [live, setLive] = useState<Device[]>([]);
  const [rates, setRates] = useState<Record<string, { kbps: number; fps: number }>>({});
  const prev = useRef<Record<string, { t: number; bytes: number; frames: number }>>({});
  const [web, setWeb] = useState<Browser[]>([]);
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
    const tickWeb = () =>
      browsers()
        .then((b) => !stop && setWeb(b ?? []))
        .catch(() => {});
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
    const both = () => (tick(), tickWeb());
    both();
    const id = setInterval(both, 2000);
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
        {web.length > 0 && (
          <p className="hint">
            Browsers for websites:{" "}
            {web
              .map((b) => `${b.Profile}: ${b.Tabs} window${b.Tabs === 1 ? "" : "s"}${b.Parked ? ` (${b.Parked} kept warm)` : ""}${b.Memory ? `, ${Math.round(b.Memory / 1048576)} MB` : ""}`)
              .join(" · ")}
          </p>
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
                  onClick={async () => {
                    if (!(await confirm("Make a new key? Every Show will be disconnected until it is given the new one.", "New key"))) return;
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

        <Speaker cfg={p.cfg} edit={p.edit} say={p.say} />
        <AutoSwitch cfg={p.cfg} edit={p.edit} say={p.say} />
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

/** Rules that give a Show another profile while an application is in front on this computer. */
function AutoSwitch(p: { cfg: m.Config; edit: (c: m.Config) => void; say: Say }) {
  const rules = p.cfg.autoSwitch ?? [];
  const profiles = Object.keys(p.cfg.profiles);
  const [waiting, setWaiting] = useState<number | null>(null);
  const put = (i: number, patch: Partial<m.AutoRule>) => p.edit(m.setAutoSwitch(p.cfg, rules.map((r, j) => (j === i ? { ...r, ...patch } : r))));
  const detect = (i: number) => {
    setWaiting(i);
    frontApp(4)
      .then((a) => put(i, { app: a.Name || a.ID }))
      .catch((e) => p.say({ kind: "err", text: errorText(e) }))
      .finally(() => setWaiting(null));
  };
  const devices = Object.keys(p.cfg.devices ?? {});
  return (
    <>
      <h3>Profile by application</h3>
      <p className="hint">While one of these applications is in front on this computer, the Show uses the profile beside it. The first rule that fits wins; with none, a Show uses its own profile. Applies once you save.</p>
      {rules.length > 0 && (
        <table className="devices">
          <tbody>
            {rules.map((r, i) => {
              const bad = m.ruleProblem(p.cfg, r);
              return (
                <tr key={i}>
                  <td>
                    <input className={bad && !r.app.trim() ? "bad" : ""} spellCheck={false} value={r.app} placeholder="Safari, code.exe…" onChange={(e) => put(i, { app: e.target.value })} />
                  </td>
                  <td>
                    <button disabled={waiting !== null} title="Switch to the application within 4 seconds" onClick={() => detect(i)}>
                      {waiting === i ? "Switch now…" : "Detect"}
                    </button>
                  </td>
                  <td>
                    <select value={r.profile} onChange={(e) => put(i, { profile: e.target.value })}>
                      {profiles.map((n) => (
                        <option key={n}>{n}</option>
                      ))}
                    </select>
                  </td>
                  <td>
                    <select value={r.device ?? ""} onChange={(e) => put(i, { device: e.target.value || undefined })}>
                      <option value="">every Show</option>
                      {[...new Set([...devices, ...(r.device ? [r.device] : [])])].map((n) => (
                        <option key={n} value={n}>
                          {n}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>
                    <button onClick={() => p.edit(m.setAutoSwitch(p.cfg, rules.filter((_, j) => j !== i)))}>✕</button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      <button onClick={() => p.edit(m.setAutoSwitch(p.cfg, [...rules, { app: "", profile: profiles[0] }]))}>Add rule</button>
    </>
  );
}

/** The Shows as this computer's speaker: which Shows, how far ahead, how long to wait in silence,
 * and a test tone. Part of the config, saved with it; the tone uses the picks as they are now. */
function Speaker(p: { cfg: m.Config; edit: (c: m.Config) => void; say: Say }) {
  const sp = p.cfg.speaker ?? { enabled: false };
  const picked = sp.shows ?? [];
  const lead = sp.lead_ms || m.SPEAKER_LEAD.default;
  const idle = sp.idle_s || m.SPEAKER_IDLE.default;
  const [found, setFound] = useState<SendspinPlayer[] | null>(null);
  const [looking, setLooking] = useState(false);
  const [testing, setTesting] = useState(false);
  const [st, setSt] = useState<SpeakerStatus | null>(null);

  const look = useCallback(() => {
    setLooking(true);
    speakerShows()
      .then((l) => setFound(l ?? []))
      .catch((e) => p.say({ kind: "err", text: errorText(e) }))
      .finally(() => setLooking(false));
  }, [p]);
  useEffect(look, []); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    let stop = false;
    const tick = () => speakerStatus().then((s) => !stop && setSt(s)).catch(() => {});
    tick();
    const id = setInterval(tick, 2000);
    return () => ((stop = true), clearInterval(id));
  }, []);

  const names = [...new Set([...(found ?? []).map((f) => f.Name), ...picked])];
  const toggle = (n: string, on: boolean) => p.edit(m.setSpeaker(p.cfg, { shows: on ? [...picked, n] : picked.filter((x) => x !== n) }));
  const test = () => {
    setTesting(true);
    speakerTestTone(picked, lead)
      .then(() => p.say({ kind: "ok", text: "The tone played on " + picked.join(", ") + "." }))
      .catch((e) => p.say({ kind: "err", text: errorText(e) }))
      .finally(() => setTesting(false));
  };

  return (
    <>
      <h3>Speaker</h3>
      <p className="hint">
        Plays this computer's sound on the Shows ticked here: pick “TECHO5 Show” as the sound output. The Shows are only taken while there is sound, and let go after the silence below.
        {st && <> Now: {st.Text}.</>}
      </p>
      <label className="row">
        <input type="checkbox" checked={sp.enabled} onChange={(e) => p.edit(m.setSpeaker(p.cfg, { enabled: e.target.checked }))} /> Play sound on Show
      </label>
      <div className="row">
        <strong>Shows</strong>
        <span className="spacer" />
        <button disabled={looking} onClick={look}>{looking ? "Looking…" : "Look again"}</button>
      </div>
      {names.length === 0 && <p className="hint">{looking ? "Looking for Shows on the network…" : "No Sendspin player answered. Is the Show on this network?"}</p>}
      {names.map((n) => (
        <label key={n} className="row">
          <input type="checkbox" checked={picked.includes(n)} onChange={(e) => toggle(n, e.target.checked)} /> {n}
          {found && !found.some((f) => f.Name === n) && <small className="hint"> (not answering now)</small>}
        </label>
      ))}
      <label>
        Lead: {lead} ms
        <input type="range" min={m.SPEAKER_LEAD.min} max={m.SPEAKER_LEAD.max} step={10} value={lead} onChange={(e) => p.edit(m.setSpeaker(p.cfg, { lead_ms: Number(e.target.value) }))} />
        <small className="hint">How far behind the computer the Show plays. Less is closer to a video's picture; too little and Wi-Fi hiccups are heard.</small>
      </label>
      <label>
        Let go after {idle} s of silence
        <input type="range" min={m.SPEAKER_IDLE.min} max={m.SPEAKER_IDLE.max} step={1} value={idle} onChange={(e) => p.edit(m.setSpeaker(p.cfg, { idle_s: Number(e.target.value) }))} />
        <small className="hint">Until then no other source, such as Music Assistant, can play on the Show.</small>
      </label>
      <button disabled={testing || picked.length === 0} onClick={test}>{testing ? "Playing…" : "Test tone"}</button>
      <p className="hint">Changes apply once you save. The test tone uses the Shows and lead as they are here.</p>
    </>
  );
}
