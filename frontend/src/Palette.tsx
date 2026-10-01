import { useDraggable } from "@dnd-kit/core";
import { useEffect, useMemo, useState } from "react";
import { lucideSVGs } from "./api";
import { Icon } from "./Icon";
import type { Schemas } from "./schema";

/** A Lucide icon for each action type, so the palette can be scanned by picture. */
const ICONS: Record<string, string> = {
  page: "folder", back: "arrow-left", "stream.web": "globe", "stream.app": "app-window",
  "open.url": "link", "open.app": "rocket", "open.file": "file", keys: "keyboard", type: "type",
  "media.play": "play", "media.next": "skip-forward", "media.prev": "skip-back",
  "volume.up": "volume-2", "volume.down": "volume-1", "volume.set": "sliders-horizontal",
  "volume.mute": "volume-x", "mic.mute": "mic-off", lock: "lock", sleep: "moon", screenshot: "camera",
  http: "webhook", "ha.service": "house", obs: "video", run: "terminal", delay: "timer", multi: "layers", toggle: "toggle-left",
};

const iconName = (type: string) => `lucide:${ICONS[type] ?? "square"}`;

/** Every kind of button, grouped, searchable; drag one onto a cell. */
export function Palette({ schemas }: { schemas: Schemas }) {
  const [q, setQ] = useState("");
  const [svgs, setSvgs] = useState<Record<string, string>>({});
  const all = useMemo(() => Object.values(schemas), [schemas]);
  useEffect(() => {
    const names = [...new Set(all.map((s) => iconName(s.type)))];
    if (names.length) lucideSVGs(names).then(setSvgs, () => {});
  }, [all]);

  const groups = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const out: Record<string, typeof all> = {};
    for (const s of all) {
      if (needle && !`${s.label} ${s.group} ${s.type}`.toLowerCase().includes(needle)) continue;
      (out[s.group] ??= []).push(s);
    }
    return Object.entries(out);
  }, [all, q]);

  return (
    <>
      <label className="search">
        <Icon name="search" size={14} />
        <input placeholder="Find an action" value={q} onChange={(e) => setQ(e.target.value)} />
      </label>
      {groups.length === 0 && <p className="hint">Nothing matches “{q}”.</p>}
      {groups.map(([g, items]) => (
        <details key={g} className="group" open>
          <summary>
            <Icon name="chevron" size={12} />
            {g}
            <span className="count">{items.length}</span>
          </summary>
          <div className="palette">
            {items.map((s) => (
              <PaletteItem key={s.type} type={s.type} label={s.label} svg={svgs[iconName(s.type)]} />
            ))}
          </div>
        </details>
      ))}
    </>
  );
}

function PaletteItem({ type, label, svg }: { type: string; label: string; svg?: string }) {
  const { setNodeRef, listeners, attributes, isDragging } = useDraggable({ id: `palette:${type}` });
  return (
    <div ref={setNodeRef} {...listeners} {...attributes} className={`tile ${isDragging ? "dragging" : ""}`} title={label}>
      <span className="svg" dangerouslySetInnerHTML={{ __html: svg ?? "" }} />
      <span className="name">{label}</span>
    </div>
  );
}

/** What follows the pointer while a palette item is dragged. */
export function PaletteGhost({ label }: { label: string }) {
  return <div className="tile ghost">{label}</div>;
}
