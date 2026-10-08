import * as m from "./model";

/** The "Live tile" part of the inspector: what the button shows big, and how often it is read. */
export function TileForm(p: { tile?: m.Tile; onChange: (t: m.Tile | null) => void }) {
  const t = p.tile;
  const set = (patch: Partial<m.Tile>) => {
    const next = { ...t!, ...patch };
    for (const k of Object.keys(next) as (keyof m.Tile)[]) if (next[k] === undefined || next[k] === "") delete next[k];
    p.onChange(next);
  };
  const problem = t ? m.tileProblem(t) : null;
  return (
    <fieldset>
      <legend>Live tile</legend>
      <label>
        Shows
        <select value={t?.type ?? ""} onChange={(e) => p.onChange(e.target.value ? { type: e.target.value } : null)}>
          <option value="">(nothing live)</option>
          {Object.entries(m.TILE_TYPES).map(([k, v]) => (
            <option key={k} value={k}>
              {v}
            </option>
          ))}
        </select>
      </label>
      {t?.type === "clock" && (
        <label>
          Format
          <select value={t.format ?? "24h"} onChange={(e) => set({ format: e.target.value === "24h" ? undefined : e.target.value })}>
            <option value="24h">14:05</option>
            <option value="12h">2:05 PM</option>
            <option value="24h-seconds">14:05:09</option>
            <option value="date">Mon 2 Jan</option>
          </select>
        </label>
      )}
      {(t?.type === "cpu_temp" || t?.type === "gpu_temp") && (
        <label>
          Unit
          <select value={t.format ?? "c"} onChange={(e) => set({ format: e.target.value === "c" ? undefined : e.target.value })}>
            <option value="c">°C</option>
            <option value="f">°F</option>
          </select>
        </label>
      )}
      {(t?.type === "net_down" || t?.type === "net_up") && (
        <label>
          Unit
          <select value={t.format ?? "bytes"} onChange={(e) => set({ format: e.target.value === "bytes" ? undefined : e.target.value })}>
            <option value="bytes">1.2 MB/s</option>
            <option value="bits">9.9 Mbit/s</option>
          </select>
        </label>
      )}
      {t?.type === "ha_state" && (
        <label>
          Entity
          <input className={problem ? "bad" : ""} spellCheck={false} value={t.entity ?? ""} placeholder="sensor.living_room_temperature" onChange={(e) => set({ entity: e.target.value })} />
          <small className="hint">The address and token are in Shows → Integrations.</small>
        </label>
      )}
      {(t?.type === "script" || t?.type === "state") && (
        <>
          <label>
            Command
            <input className={problem ? "bad" : ""} spellCheck={false} value={t.command ?? ""} onChange={(e) => set({ command: e.target.value })} />
          </label>
          <label>
            Arguments
            <textarea rows={2} spellCheck={false} value={(t.args ?? []).join("\n")} onChange={(e) => set({ args: e.target.value ? e.target.value.split("\n") : undefined })} />
            <small className="hint">One per line</small>
          </label>
          <label>
            <input type="checkbox" checked={!!t.shell} onChange={(e) => set({ shell: e.target.checked || undefined })} /> Run as a shell line
          </label>
          <small className="hint">
            {t.type === "script"
              ? "The first line it prints is the value."
              : "On when it exits 0 and prints nothing but yes, true, on or 1; off when it fails or prints no, false, off or 0. A toggle button then flips from what is true."}
          </small>
        </>
      )}
      {t && (
        <label>
          Every (seconds)
          <input type="number" min={1} max={3600} value={t.every ?? ""} placeholder="default" onChange={(e) => set({ every: e.target.value === "" ? undefined : Number(e.target.value) })} />
        </label>
      )}
      {problem && <p className="msg err" style={{ cursor: "default" }}>{problem}</p>}
    </fieldset>
  );
}
