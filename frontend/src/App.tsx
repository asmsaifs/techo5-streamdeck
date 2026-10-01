import { DndContext, DragOverlay, PointerSensor, useDraggable, useDroppable, useSensor, useSensors } from "@dnd-kit/core";
import type { DragEndEvent } from "@dnd-kit/core";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { errorText, loadConfig, preview, saveConfig } from "./api";
import { cellRects } from "./layout";
import * as m from "./model";

// The Show 5's screen. The preview is drawn at this size; a Show of another size gets the same
// layout scaled (step 2.4 adds a device picker).
const SCREEN = { w: 960, h: 480 };

export function App() {
  const [hist, setHist] = useState<m.History | null>(null);
  const [savedCfg, setSaved] = useState<m.Config | null>(null);
  const [profile, setProfile] = useState("default");
  const [page, setPage] = useState(m.HOME);
  const [sel, setSel] = useState<string | null>(null);
  const [clip, setClip] = useState<m.Button | null>(null);
  const [msg, setMsg] = useState<{ kind: "err" | "ok"; text: string } | null>(null);

  useEffect(() => {
    loadConfig()
      .then((c) => {
        setHist(m.start(c));
        setSaved(c);
        setProfile(c.profiles.default ? "default" : Object.keys(c.profiles)[0]);
      })
      .catch((e) => setMsg({ kind: "err", text: errorText(e) }));
  }, []);

  const cfg = hist?.present ?? null;
  const edit = useCallback((next: m.Config) => setHist((h) => (h ? m.commit(h, next) : h)), []);
  const dirty = cfg !== null && cfg !== savedCfg;

  const save = useCallback(async () => {
    if (!cfg) return;
    try {
      await saveConfig(cfg);
      setSaved(cfg);
      setMsg({ kind: "ok", text: "Saved. The Show is updated." });
    } catch (e) {
      setMsg({ kind: "err", text: errorText(e) });
    }
  }, [cfg]);

  const prof = cfg?.profiles[profile];
  const pg = prof?.pages[page];

  // A page that was deleted or renamed away, or a profile switch, falls back to home.
  useEffect(() => {
    if (prof && !prof.pages[page]) setPage(m.HOME);
  }, [prof, page]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const inField = (e.target as HTMLElement).closest("input, textarea, select");
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key === "s") {
        e.preventDefault();
        save();
      } else if (mod && e.key === "z" && !inField) {
        e.preventDefault();
        setHist((h) => (h ? (e.shiftKey ? m.redo(h) : m.undo(h)) : h));
      } else if (mod && e.key === "y" && !inField) {
        setHist((h) => (h ? m.redo(h) : h));
      } else if (!inField && sel && prof && pg) {
        const b = pg.buttons[sel];
        if (mod && e.key === "c" && b) setClip(b);
        else if (mod && e.key === "x" && b) {
          setClip(b);
          edit(m.setButton(cfg!, profile, page, sel, null));
        } else if (mod && e.key === "v" && clip) edit(m.setButton(cfg!, profile, page, sel, clip));
        else if (e.key === "Delete" || e.key === "Backspace") edit(m.setButton(cfg!, profile, page, sel, null));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [save, sel, prof, pg, cfg, profile, page, clip, edit]);

  if (!cfg || !prof || !pg) return <div className="boot">{msg ? <p className="msg err">{msg.text}</p> : "Loading…"}</div>;

  return (
    <div className="app">
      <header>
        <select value={profile} onChange={(e) => (setProfile(e.target.value), setPage(m.HOME), setSel(null))}>
          {Object.keys(cfg.profiles).map((p) => (
            <option key={p}>{p}</option>
          ))}
        </select>
        <span className="spacer" />
        <button disabled={!hist || !hist.past.length} onClick={() => setHist(m.undo(hist!))}>
          Undo
        </button>
        <button disabled={!hist || !hist.future.length} onClick={() => setHist(m.redo(hist!))}>
          Redo
        </button>
        <button className="primary" disabled={!dirty} onClick={save}>
          {dirty ? "Save" : "Saved"}
        </button>
      </header>
      {msg && (
        <p className={`msg ${msg.kind}`} onClick={() => setMsg(null)}>
          {msg.text}
        </p>
      )}
      <main>
        <Pages cfg={cfg} profile={profile} page={page} setPage={(p) => (setPage(p), setSel(null))} edit={edit} say={setMsg} />
        <Canvas cfg={cfg} profile={profile} page={page} sel={sel} setSel={setSel} edit={edit} />
        <aside>
          <Inspector cfg={cfg} profile={profile} page={page} sel={sel} edit={edit} />
          <ProfilePanel cfg={cfg} profile={profile} edit={edit} say={setMsg} />
        </aside>
      </main>
    </div>
  );
}

function Pages(p: {
  cfg: m.Config;
  profile: string;
  page: string;
  setPage: (n: string) => void;
  edit: (c: m.Config) => void;
  say: (x: { kind: "err" | "ok"; text: string } | null) => void;
}) {
  const prof = p.cfg.profiles[p.profile];
  const ask = (q: string, def = "") => window.prompt(q, def)?.trim() ?? null;
  const refuse = (text: string | null) => (text ? (p.say({ kind: "err", text }), true) : false);
  return (
    <nav>
      <h3>Pages</h3>
      <ul>
        {Object.keys(prof.pages)
          .sort((a, b) => (a === m.HOME ? -1 : b === m.HOME ? 1 : a.localeCompare(b)))
          .map((n) => (
            <li key={n} className={n === p.page ? "on" : ""} onClick={() => p.setPage(n)}>
              {n}
            </li>
          ))}
      </ul>
      <div className="row">
        <button
          onClick={() => {
            const n = ask("New page name");
            if (n !== null && !refuse(m.pageNameProblem(prof, n))) {
              p.edit(m.addPage(p.cfg, p.profile, n));
              p.setPage(n);
            }
          }}
        >
          Add
        </button>
        <button
          disabled={p.page === m.HOME}
          onClick={() => {
            const n = ask("Rename page to", p.page);
            if (n !== null && n !== p.page && !refuse(m.pageNameProblem(prof, n))) {
              p.edit(m.renamePage(p.cfg, p.profile, p.page, n));
              p.setPage(n);
            }
          }}
        >
          Rename
        </button>
        <button
          onClick={() => {
            if (!refuse(m.deleteBlocker(prof, p.page)) && window.confirm(`Delete page "${p.page}"?`)) {
              p.edit(m.deletePage(p.cfg, p.profile, p.page));
              p.setPage(m.HOME);
            }
          }}
        >
          Delete
        </button>
      </div>
      <h3>Add a button</h3>
      <p className="hint">Drag onto a cell.</p>
      <div className="palette">
        {m.ACTION_TYPES.map((t) => (
          <PaletteItem key={t} type={t} />
        ))}
      </div>
    </nav>
  );
}

function PaletteItem({ type }: { type: string }) {
  const { setNodeRef, listeners, attributes, isDragging } = useDraggable({ id: `palette:${type}` });
  return (
    <div ref={setNodeRef} {...listeners} {...attributes} className={`chip ${isDragging ? "dragging" : ""}`}>
      {m.PALETTE_LABELS[type]}
    </div>
  );
}

function Canvas(p: {
  cfg: m.Config;
  profile: string;
  page: string;
  sel: string | null;
  setSel: (k: string | null) => void;
  edit: (c: m.Config) => void;
}) {
  const prof = p.cfg.profiles[p.profile];
  const pg = prof.pages[p.page];
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }));
  const [img, setImg] = useState<string>("");
  const [err, setErr] = useState("");
  const [dragId, setDragId] = useState<string | null>(null);
  const rects = useMemo(() => cellRects(prof.grid, SCREEN.w, SCREEN.h), [prof.grid]);

  // The preview is the Go renderer's picture of the unsaved config. Only the newest answer is
  // shown, and edits that follow each other fast are drawn once.
  const seq = useRef(0);
  useEffect(() => {
    const mine = ++seq.current;
    const t = setTimeout(() => {
      preview(p.cfg, p.profile, p.page, SCREEN.w, SCREEN.h)
        .then((u) => mine === seq.current && (setImg(u), setErr("")))
        .catch((e) => mine === seq.current && setErr(errorText(e)));
    }, 60);
    return () => clearTimeout(t);
  }, [p.cfg, p.profile, p.page]);

  const end = (e: DragEndEvent) => {
    setDragId(null);
    const from = String(e.active.id);
    const to = e.over ? String(e.over.id) : null;
    if (!to?.startsWith("cell:")) return;
    const key = to.slice(5);
    if (from.startsWith("palette:")) {
      const first = Object.keys(prof.pages).find((n) => n !== p.page);
      p.edit(m.setButton(p.cfg, p.profile, p.page, key, m.newButton(from.slice(8), first)));
    } else if (from.startsWith("cell:")) {
      p.edit(m.moveButton(p.cfg, p.profile, p.page, from.slice(5), key));
      if (p.sel === from.slice(5)) p.setSel(key);
      return;
    }
    p.setSel(key);
  };

  return (
    <DndContext sensors={sensors} onDragStart={(e) => setDragId(String(e.active.id))} onDragEnd={end} onDragCancel={() => setDragId(null)}>
      <section className="canvas-wrap">
        <div className="canvas" style={{ width: SCREEN.w, height: SCREEN.h, background: prof.theme.bg }} onClick={() => p.setSel(null)}>
          {img && <img src={img} width={SCREEN.w} height={SCREEN.h} draggable={false} alt="" />}
          {rects.map((r, i) => {
            const key = m.cellKey(i % prof.grid.cols, Math.floor(i / prof.grid.cols));
            return <Cell key={key} id={key} rect={r} full={!!pg.buttons[key]} on={p.sel === key} pick={() => p.setSel(key)} />;
          })}
        </div>
        {err && <p className="msg err">{err}</p>}
        <p className="hint">Click a cell to edit it. Drag to move or swap. ⌘C / ⌘V copy and paste, ⌫ clears, ⌘Z undoes.</p>
      </section>
      <DragOverlay>{dragId?.startsWith("palette:") ? <div className="chip">{m.PALETTE_LABELS[dragId.slice(8)]}</div> : null}</DragOverlay>
    </DndContext>
  );
}

function Cell(p: { id: string; rect: { x: number; y: number; w: number; h: number }; full: boolean; on: boolean; pick: () => void }) {
  const drop = useDroppable({ id: `cell:${p.id}` });
  const drag = useDraggable({ id: `cell:${p.id}`, disabled: !p.full });
  return (
    <div
      ref={(n) => (drop.setNodeRef(n), drag.setNodeRef(n))}
      {...drag.listeners}
      {...drag.attributes}
      className={`cell ${p.on ? "on" : ""} ${drop.isOver ? "over" : ""} ${drag.isDragging ? "lifted" : ""}`}
      style={{ left: p.rect.x, top: p.rect.y, width: p.rect.w, height: p.rect.h }}
      onClick={(e) => (e.stopPropagation(), p.pick())}
    />
  );
}

function Inspector(p: { cfg: m.Config; profile: string; page: string; sel: string | null; edit: (c: m.Config) => void }) {
  const b = p.sel ? p.cfg.profiles[p.profile].pages[p.page].buttons[p.sel] : undefined;
  const [json, setJson] = useState("");
  const [bad, setBad] = useState(false);
  const actionJSON = b?.action ? JSON.stringify(b.action, null, 2) : "";
  // The text box follows the button picked, and what the button holds after an undo.
  useEffect(() => (setJson(actionJSON), setBad(false)), [actionJSON, p.sel]);

  if (!p.sel) return <section className="panel"><h3>Button</h3><p className="hint">Pick a cell.</p></section>;
  const set = (patch: Partial<m.Button>) => p.edit(m.setButton(p.cfg, p.profile, p.page, p.sel!, { ...(b ?? {}), ...patch }));
  const setAction = (a: m.Action | undefined) => {
    const next = { ...(b ?? {}) };
    if (a) next.action = a;
    else delete next.action;
    p.edit(m.setButton(p.cfg, p.profile, p.page, p.sel!, next));
  };
  return (
    <section className="panel">
      <h3>Button {p.sel}</h3>
      <label>
        Label
        <input value={b?.label ?? ""} onChange={(e) => set({ label: e.target.value || undefined })} />
      </label>
      <label>
        Icon
        <input
          placeholder="lucide:folder, or a file in icons/"
          value={b?.icon ?? ""}
          onChange={(e) => set({ icon: e.target.value || undefined })}
        />
      </label>
      <label>
        Action
        <select
          value={b?.action?.type ?? ""}
          onChange={(e) => {
            const t = e.target.value;
            if (!t) return setAction(undefined);
            const first = Object.keys(p.cfg.profiles[p.profile].pages).find((n) => n !== p.page);
            setAction(m.newButton(t, first).action);
          }}
        >
          <option value="">(none)</option>
          {m.ACTION_TYPES.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
      </label>
      {b?.action && (
        <label>
          Parameters (JSON; real forms come in step 2.3)
          <textarea
            className={bad ? "bad" : ""}
            rows={9}
            spellCheck={false}
            value={json}
            onChange={(e) => {
              setJson(e.target.value);
              try {
                const a = JSON.parse(e.target.value);
                if (typeof a?.type !== "string") throw new Error("no type");
                setBad(false);
                setAction(a);
              } catch {
                setBad(true);
              }
            }}
          />
        </label>
      )}
      <button onClick={() => p.edit(m.setButton(p.cfg, p.profile, p.page, p.sel!, null))} disabled={!b}>
        Clear button
      </button>
    </section>
  );
}

function ProfilePanel(p: {
  cfg: m.Config;
  profile: string;
  edit: (c: m.Config) => void;
  say: (x: { kind: "err" | "ok"; text: string } | null) => void;
}) {
  const prof = p.cfg.profiles[p.profile];
  const grid = (k: keyof m.Grid, min: number, max: number) => (
    <label key={k}>
      {k}
      <input
        type="number"
        min={min}
        max={max}
        value={prof.grid[k]}
        onChange={(e) => {
          const n = Math.max(min, Math.min(max, Math.round(Number(e.target.value))));
          if (Number.isNaN(n)) return;
          const cols = k === "cols" ? n : prof.grid.cols;
          const rows = k === "rows" ? n : prof.grid.rows;
          const why = m.shrinkBlocker(prof, cols, rows);
          if (why) return p.say({ kind: "err", text: why });
          p.edit(m.setGrid(p.cfg, p.profile, { [k]: n }));
        }}
      />
    </label>
  );
  return (
    <section className="panel">
      <h3>Profile “{p.profile}”</h3>
      <div className="two">
        {grid("cols", 1, 8)}
        {grid("rows", 1, 6)}
        {grid("gap", 0, 64)}
        {grid("radius", 0, 128)}
      </div>
      <div className="two">
        {(["bg", "button", "text", "accent"] as const).map((k) => (
          <label key={k}>
            {k}
            <input type="color" value={expandHex(prof.theme[k])} onChange={(e) => p.edit(m.setTheme(p.cfg, p.profile, { [k]: e.target.value }))} />
          </label>
        ))}
      </div>
    </section>
  );
}

/** <input type=color> wants #rrggbb; the config also allows #rgb. */
function expandHex(c: string) {
  return /^#[0-9a-f]{3}$/i.test(c) ? "#" + [...c.slice(1)].map((x) => x + x).join("") : c;
}
