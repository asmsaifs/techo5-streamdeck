import { DndContext, DragOverlay, PointerSensor, useDraggable, useDroppable, useSensor, useSensors } from "@dnd-kit/core";
import type { DragEndEvent } from "@dnd-kit/core";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActionForm, KeysInput } from "./ActionForm";
import { errorText, hotkeyProblems, loadConfig, loadSchemas, preview, saveConfig, testAction } from "./api";
import { DevicesPanel } from "./DevicesPanel";
import { IconPicker } from "./IconPicker";
import { TileForm } from "./TileForm";
import { cellRects } from "./layout";
import * as m from "./model";
import { actionProblems, blankAction, bySchemaType, firstProblem } from "./schema";
import type { Schemas } from "./schema";

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
  const [schemas, setSchemas] = useState<Schemas>({});
  const [showDevices, setShowDevices] = useState(false);
  const [dragId, setDragId] = useState<string | null>(null);
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }));
  const onDrop = useRef<(e: DragEndEvent) => void>(() => {});
  // What the system refused of the saved hotkeys, by combo.
  const [hkProblems, setHkProblems] = useState<Record<string, string>>({});
  const loadHkProblems = useCallback(() => {
    // Registering happens a moment after the save, off the save's own call.
    setTimeout(() => hotkeyProblems().then(setHkProblems).catch(() => {}), 300);
  }, []);

  useEffect(() => {
    loadSchemas()
      .then((l) => setSchemas(bySchemaType(l)))
      .catch((e) => setMsg({ kind: "err", text: errorText(e) }));
    loadConfig()
      .then((c) => {
        setHist(m.start(c));
        setSaved(c);
        setProfile(c.profiles.default ? "default" : Object.keys(c.profiles)[0]);
        loadHkProblems();
      })
      .catch((e) => setMsg({ kind: "err", text: errorText(e) }));
  }, []);

  const cfg = hist?.present ?? null;
  const edit = useCallback((next: m.Config) => setHist((h) => (h ? m.commit(h, next) : h)), []);
  const dirty = cfg !== null && cfg !== savedCfg;

  const save = useCallback(async () => {
    if (!cfg) return;
    const bad = firstProblem(schemas, cfg);
    if (bad) return setMsg({ kind: "err", text: bad });
    // A run button can do anything the user can; show the exact lines before they are first saved.
    const cmds = savedCfg ? m.newRunCommands(savedCfg, cfg) : [];
    if (cmds.length && !window.confirm(`These commands will run on this computer when their buttons are pressed:\n\n${cmds.join("\n")}\n\nSave?`)) return;
    try {
      await saveConfig(cfg);
      setSaved(cfg);
      loadHkProblems();
      setMsg({ kind: "ok", text: "Saved. The Show is updated." });
    } catch (e) {
      setMsg({ kind: "err", text: errorText(e) });
    }
  }, [cfg, savedCfg, schemas, loadHkProblems]);

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
        <button
          onClick={() => {
            const n = window.prompt(`New profile (a copy of "${profile}")`)?.trim();
            if (n === undefined || n === null) return;
            const why = m.profileNameProblem(cfg, n);
            if (why) return setMsg({ kind: "err", text: why });
            edit(m.addProfile(cfg, n, profile));
            setProfile(n);
            setPage(m.HOME);
          }}
        >
          New profile
        </button>
        <button
          onClick={() => {
            const why = m.profileDeleteBlocker(cfg, profile);
            if (why) return setMsg({ kind: "err", text: why });
            if (!window.confirm(`Delete profile "${profile}"?`)) return;
            edit(m.deleteProfile(cfg, profile));
            setProfile("default");
            setPage(m.HOME);
          }}
        >
          Delete profile
        </button>
        <span className="spacer" />
        <button onClick={() => setShowDevices(true)}>Shows…</button>
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
      {showDevices && <DevicesPanel cfg={cfg} edit={edit} say={setMsg} onClose={() => setShowDevices(false)} />}
      {msg && (
        <p className={`msg ${msg.kind}`} onClick={() => setMsg(null)}>
          {msg.text}
        </p>
      )}
      {/* One drag context for the palette (in Pages) and the cells (in Canvas): a button dragged from
          the palette must be able to land on a cell. The canvas says what a drop means. */}
      <DndContext sensors={sensors} onDragStart={(e) => setDragId(String(e.active.id))} onDragEnd={(e) => (setDragId(null), onDrop.current(e))} onDragCancel={() => setDragId(null)}>
        <main>
          <Pages cfg={cfg} profile={profile} page={page} setPage={(p) => (setPage(p), setSel(null))} edit={edit} say={setMsg} schemas={schemas} />
          <Canvas cfg={cfg} profile={profile} page={page} sel={sel} setSel={setSel} edit={edit} schemas={schemas} onDrop={onDrop} />
          <aside>
            <Inspector cfg={cfg} profile={profile} page={page} sel={sel} edit={edit} schemas={schemas} hkProblems={hkProblems} />
            <ProfilePanel cfg={cfg} profile={profile} edit={edit} say={setMsg} />
          </aside>
        </main>
        <DragOverlay>{dragId?.startsWith("palette:") ? <div className="chip">{schemas[dragId.slice(8)]?.label}</div> : null}</DragOverlay>
      </DndContext>
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
  schemas: Schemas;
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
        {Object.values(p.schemas).map((x) => (
          <PaletteItem key={x.type} type={x.type} label={x.label} />
        ))}
      </div>
    </nav>
  );
}

function PaletteItem({ type, label }: { type: string; label: string }) {
  const { setNodeRef, listeners, attributes, isDragging } = useDraggable({ id: `palette:${type}` });
  return (
    <div ref={setNodeRef} {...listeners} {...attributes} className={`chip ${isDragging ? "dragging" : ""}`}>
      {label}
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
  schemas: Schemas;
  onDrop: { current: (e: DragEndEvent) => void };
}) {
  const prof = p.cfg.profiles[p.profile];
  const pg = prof.pages[p.page];
  const [img, setImg] = useState<string>("");
  const [err, setErr] = useState("");
  const rects = useMemo(() => cellRects(prof.grid, SCREEN.w, SCREEN.h), [prof.grid]);

  // The screen is drawn at its real size and shrunk to fit the space the window gives it, so the
  // picture never needs a scroll bar. It is never enlarged: the preview is a bitmap.
  const wrap = useRef<HTMLElement>(null);
  const [scale, setScale] = useState(1);
  useEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setScale(Math.min(1, (el.clientWidth - 32) / SCREEN.w)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

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

  p.onDrop.current = (e: DragEndEvent) => {
    const from = String(e.active.id);
    const to = e.over ? String(e.over.id) : null;
    if (!to?.startsWith("cell:")) return;
    const key = to.slice(5);
    if (from.startsWith("palette:")) {
      const type = from.slice(8);
      const pages = Object.keys(prof.pages).filter((n) => n !== p.page);
      p.edit(m.setButton(p.cfg, p.profile, p.page, key, { label: p.schemas[type]?.label ?? type, action: blankAction(p.schemas, type, pages) }));
    } else if (from.startsWith("cell:")) {
      p.edit(m.moveButton(p.cfg, p.profile, p.page, from.slice(5), key));
      if (p.sel === from.slice(5)) p.setSel(key);
      return;
    }
    p.setSel(key);
  };

  return (
    <>
      <section className="canvas-wrap" ref={wrap}>
        <div style={{ width: SCREEN.w * scale, height: SCREEN.h * scale }}>
        <div className="canvas" style={{ width: SCREEN.w, height: SCREEN.h, background: prof.theme.bg, transform: `scale(${scale})`, transformOrigin: "0 0" }} onClick={() => p.setSel(null)}>
          {img && <img src={img} width={SCREEN.w} height={SCREEN.h} draggable={false} alt="" />}
          {rects.map((r, i) => {
            const key = m.cellKey(i % prof.grid.cols, Math.floor(i / prof.grid.cols));
            return <Cell key={key} id={key} rect={r} full={!!pg.buttons[key]} on={p.sel === key} pick={() => p.setSel(key)} />;
          })}
        </div>
        </div>
        {err && <p className="msg err">{err}</p>}
        <p className="hint">Click a cell to edit it. Drag to move or swap. ⌘C / ⌘V copy and paste, ⌫ clears, ⌘Z undoes.</p>
      </section>
    </>
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

function Inspector(p: { cfg: m.Config; profile: string; page: string; sel: string | null; edit: (c: m.Config) => void; schemas: Schemas; hkProblems: Record<string, string> }) {
  const prof = p.cfg.profiles[p.profile];
  const b = p.sel ? prof.pages[p.page].buttons[p.sel] : undefined;
  const [picking, setPicking] = useState(false);
  const [moved, setMoved] = useState<string | null>(null);
  const [asJSON, setAsJSON] = useState(false);
  const [json, setJson] = useState("");
  const [bad, setBad] = useState(false);
  const [test, setTest] = useState<{ kind: "err" | "ok" | "wait"; text: string } | null>(null);
  const actionJSON = b?.action ? JSON.stringify(b.action, null, 2) : "";
  // The JSON box follows the button picked, and what the button holds after an undo.
  useEffect(() => (setJson(actionJSON), setBad(false)), [actionJSON, p.sel]);
  useEffect(() => (setTest(null), setMoved(null)), [p.sel, p.page]);

  if (!p.sel) return <section className="panel"><h3>Button</h3><p className="hint">Pick a cell.</p></section>;
  const sel = p.sel;
  const put = (next: m.Button | null) => p.edit(m.setButton(p.cfg, p.profile, p.page, sel, next));
  const set = (patch: Partial<m.Button>) => put({ ...(b ?? {}), ...patch });
  const setAction = (a: m.Action | undefined) => {
    const next = { ...(b ?? {}) };
    if (a) next.action = a;
    else delete next.action;
    put(next);
  };
  const schema = b?.action ? p.schemas[b.action.type] : undefined;
  const problems = b?.action ? actionProblems(p.schemas, b.action) : [];
  const pages = Object.keys(prof.pages).filter((n) => n !== p.page);
  const userFile = b?.icon && !b.icon.startsWith("lucide:") ? b.icon : "";
  // Folders and Back move around on a Show's screen, so a hotkey cannot press them.
  const hotkeyable = !!b?.action && b.action.type !== "page" && b.action.type !== "back";
  const hotkey = m.hotkeyOf(p.cfg, p.profile, p.page, sel) ?? "";

  const run = async () => {
    if (!b?.action) return;
    setTest({ kind: "wait", text: "Running…" });
    try {
      await testAction(b.action);
      setTest({ kind: "ok", text: "Done." });
    } catch (e) {
      setTest({ kind: "err", text: errorText(e) });
    }
  };

  return (
    <section className="panel">
      <h3>Button {sel}</h3>
      <label>
        Label
        <input value={b?.label ?? ""} onChange={(e) => set({ label: e.target.value || undefined })} />
      </label>
      <label>
        Icon
        <button className="iconbtn" onClick={() => setPicking(true)}>
          {b?.icon ? b.icon.replace(/^lucide:/, "") : "Choose…"}
        </button>
        {userFile && !userFile.match(/\.(png|jpe?g)$/i) && <small className="hint">Emoji are not drawn yet; pick a picture or an icon.</small>}
      </label>
      {picking && (
        <IconPicker
          value={b?.icon}
          onClose={() => setPicking(false)}
          onPick={(icon) => (set({ icon }), setPicking(false))}
        />
      )}

      <TileForm tile={b?.tile} onChange={(t) => put(m.setTile(b ?? {}, t))} />

      {b?.action ? (
        <ActionForm
          schemas={p.schemas}
          pages={Object.keys(prof.pages)}
          action={b.action}
          onChange={setAction}
          allowNone
          onNone={() => setAction(undefined)}
        />
      ) : (
        <label>
          Action
          <select value="" onChange={(e) => e.target.value && setAction(blankAction(p.schemas, e.target.value, pages))}>
            <option value="">(none)</option>
            {Object.values(p.schemas).map((x) => (
              <option key={x.type} value={x.type}>
                {x.label}
              </option>
            ))}
          </select>
        </label>
      )}

      {b?.action?.type === "run" && b.action.command ? (
        <p className="cmd">{b.action.shell ? `sh -c ${b.action.command}` : [b.action.command, ...((b.action.args as string[]) ?? [])].join(" ")}</p>
      ) : null}
      {hotkeyable && (
        <>
          <KeysInput
            label="Global hotkey"
            miss={false}
            value={hotkey}
            set={(v) => {
              const combo = String(v).trim() || null;
              const from = combo ? m.hotkeyTakenBy(p.cfg, combo, p.profile, p.page, sel) : null;
              setMoved(from ? `${combo} was on ${from}; it is on this button now.` : null);
              p.edit(m.setHotkey(p.cfg, p.profile, p.page, sel, combo));
            }}
            help={<small className="hint">Presses this button from anywhere on this computer. Leave empty for none.</small>}
          />
          {hotkey && p.hkProblems[hotkey] && <p className="msg err" style={{ cursor: "default" }}>{p.hkProblems[hotkey]}</p>}
        </>
      )}
      {moved && <p className="hint">{moved}</p>}
      {problems.map((x) => (
        <p key={x} className="msg err" style={{ cursor: "default" }}>
          {x}
        </p>
      ))}

      <div className="row">
        {schema?.runnable && (
          <button disabled={problems.length > 0 || test?.kind === "wait"} onClick={run} title="Runs it now on this computer">
            Test
          </button>
        )}
        <button onClick={() => put(null)} disabled={!b}>
          Clear button
        </button>
      </div>
      {test && <p className={`testout ${test.kind === "wait" ? "" : test.kind}`}>{test.text}</p>}

      {b?.action && (
        <details open={asJSON} onToggle={(e) => setAsJSON(e.currentTarget.open)}>
          <summary className="hint">Edit as JSON</summary>
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
        </details>
      )}
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
