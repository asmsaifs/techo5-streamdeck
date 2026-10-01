// The config as the editor edits it, and the pure operations on it. Everything here returns a new
// config and never changes the one it is given, so undo is a stack of old configs.

export interface Action {
  type: string;
  [param: string]: unknown;
}
export interface Button {
  label?: string;
  icon?: string;
  action?: Action;
  tile?: Tile;
}

/** What a live button shows: a value read again every few seconds. */
export interface Tile {
  type: string;
  every?: number;
  format?: string;
  entity?: string;
  command?: string;
  args?: string[];
  shell?: boolean;
}

export const TILE_TYPES: Record<string, string> = {
  clock: "Clock",
  cpu: "Processor load",
  ram: "Memory use",
  ha_state: "Home Assistant state",
  script: "Command output",
  state: "Toggle state (a command says if it is on)",
};

/** What a tile of this type needs before it is worth saving, or null. */
export function tileProblem(t: Tile): string | null {
  if (t.every !== undefined && (t.every < 1 || t.every > 3600)) return "Every must be 1 to 3600 seconds.";
  if (t.type === "ha_state" && !/^[a-z0-9_]+\.[a-z0-9_]+$/.test(t.entity ?? "")) return "Entity looks like sensor.kitchen_temperature.";
  if ((t.type === "script" || t.type === "state") && !(t.command ?? "").trim()) return "A command is needed.";
  return null;
}

/** Sets or, for null, removes a button's tile. */
export function setTile(b: Button, t: Tile | null): Button {
  const next = { ...b };
  if (t) next.tile = t;
  else delete next.tile;
  return next;
}
export interface Page {
  buttons: Record<string, Button>;
}
export interface Grid {
  cols: number;
  rows: number;
  gap: number;
  radius: number;
}
export interface Theme {
  bg: string;
  button: string;
  text: string;
  accent: string;
}
export interface Profile {
  grid: Grid;
  theme: Theme;
  pages: Record<string, Page>;
}
export interface Config {
  version: number;
  server: { listen: string; key: string };
  devices?: Record<string, { profile: string }>;
  profiles: Record<string, Profile>;
  hotkeys?: Record<string, HotkeyTarget>;
  integrations?: Integrations;
}

/** Where the Home Assistant and OBS actions connect. Their token and password are not here: they
 * are in the system keychain. */
export interface Integrations {
  homeassistant?: string;
  obs?: string;
}

/** The button a global hotkey presses. */
export interface HotkeyTarget {
  profile: string;
  page: string;
  button: string;
}

/** Sets one integration's address; an empty one is removed, and so is the section when it is bare. */
export function setIntegration(cfg: Config, key: keyof Integrations, value: string): Config {
  const next = clone(cfg);
  const all = { ...(next.integrations ?? {}) };
  if (value.trim()) all[key] = value.trim();
  else delete all[key];
  if (Object.keys(all).length) next.integrations = all;
  else delete next.integrations;
  return next;
}

export const HOME = "home";

export const cellKey = (col: number, row: number) => `${col},${row}`;

export function parseCell(key: string): [number, number] {
  const [c, r] = key.split(",").map(Number);
  return [c, r];
}

const clone = <T,>(v: T): T => structuredClone(v);

/** Changes one page of one profile through fn, which edits a private copy. */
function editPage(cfg: Config, profile: string, page: string, fn: (p: Page) => void): Config {
  const next = clone(cfg);
  const pg = next.profiles[profile]?.pages[page];
  if (!pg) return cfg;
  fn(pg);
  return next;
}

export function setButton(cfg: Config, profile: string, page: string, key: string, b: Button | null): Config {
  const next = editPage(cfg, profile, page, (pg) => {
    if (b) pg.buttons[key] = clone(b);
    else delete pg.buttons[key];
  });
  // A hotkey for a button that is gone would make the config invalid.
  return b ? next : retarget(next, (t) => (t.profile === profile && t.page === page && t.button === key ? null : t));
}

/** Moves the button at `from` to `to`; if `to` has one, they swap. */
export function moveButton(cfg: Config, profile: string, page: string, from: string, to: string): Config {
  if (from === to) return cfg;
  const next = editPage(cfg, profile, page, (pg) => {
    const a = pg.buttons[from];
    const b = pg.buttons[to];
    if (a) pg.buttons[to] = a;
    else delete pg.buttons[to];
    if (b) pg.buttons[from] = b;
    else delete pg.buttons[from];
  });
  // A hotkey follows its button to where it went.
  return retarget(next, (t) => {
    if (t.profile !== profile || t.page !== page) return t;
    if (t.button === from) return { ...t, button: to };
    if (t.button === to) return { ...t, button: from };
    return t;
  });
}

/** Rewrites every hotkey's target through fn, which returns the new one or null to drop it. */
function retarget(cfg: Config, fn: (t: HotkeyTarget) => HotkeyTarget | null): Config {
  if (!cfg.hotkeys) return cfg;
  const out: Record<string, HotkeyTarget> = {};
  for (const [combo, t] of Object.entries(cfg.hotkeys)) {
    const n = fn(t);
    if (n) out[combo] = n;
  }
  const { hotkeys: _old, ...rest } = cfg;
  return Object.keys(out).length ? { ...rest, hotkeys: out } : rest;
}

/** The global hotkey of a button, or undefined. */
export function hotkeyOf(cfg: Config, profile: string, page: string, key: string): string | undefined {
  return Object.entries(cfg.hotkeys ?? {}).find(([, t]) => t.profile === profile && t.page === page && t.button === key)?.[0];
}

/** Gives a button the global hotkey `combo`, or none for null. A button has at most one; a combo
 * belongs to one button, so it is taken from whichever had it. */
export function setHotkey(cfg: Config, profile: string, page: string, key: string, combo: string | null): Config {
  const mine = (t: HotkeyTarget) => t.profile === profile && t.page === page && t.button === key;
  const next = clone(cfg);
  const kept: Record<string, HotkeyTarget> = {};
  for (const [c, t] of Object.entries(next.hotkeys ?? {})) if (!mine(t) && c !== combo) kept[c] = t;
  if (combo) kept[combo] = { profile, page, button: key };
  if (Object.keys(kept).length) next.hotkeys = kept;
  else delete next.hotkeys;
  return next;
}

/** Who already has `combo`, as "profile / page / cell", when it is not the given button. */
export function hotkeyTakenBy(cfg: Config, combo: string, profile: string, page: string, key: string): string | null {
  const t = cfg.hotkeys?.[combo];
  if (!t || (t.profile === profile && t.page === page && t.button === key)) return null;
  return `${t.profile} / ${t.page} / ${t.button}`;
}

/** A new button for an action type dropped from the palette. */
export function newButton(type: string, firstPage?: string): Button {
  const action: Action = { type };
  if (type === "page" && firstPage) action.page = firstPage;
  return { label: PALETTE_LABELS[type] ?? type, action };
}

export const PALETTE_LABELS: Record<string, string> = {
  page: "Folder",
  back: "Back",
  "open.url": "Open site",
  "open.app": "Open app",
  "open.file": "Open file",
  keys: "Hotkey",
  "media.play": "Play / pause",
  "media.next": "Next track",
  "media.prev": "Previous track",
  "volume.up": "Volume up",
  "volume.down": "Volume down",
  "volume.set": "Set volume",
  "volume.mute": "Mute sound",
  "mic.mute": "Mute microphone",
  http: "Web request",
  "ha.service": "Home Assistant",
  obs: "OBS",
  lock: "Lock screen",
  sleep: "Sleep",
  screenshot: "Screenshot",
  type: "Type text",
  run: "Run command",
  delay: "Delay",
  multi: "Multi",
  toggle: "Toggle",
};
export const ACTION_TYPES = Object.keys(PALETTE_LABELS);

/** Why the grid cannot shrink to cols by rows, or null when it can. */
export function shrinkBlocker(p: Profile, cols: number, rows: number): string | null {
  for (const [name, pg] of Object.entries(p.pages)) {
    for (const key of Object.keys(pg.buttons)) {
      const [c, r] = parseCell(key);
      if (c >= cols || r >= rows) return `Page "${name}" has a button at ${key}, outside ${cols}×${rows}. Move or delete it first.`;
    }
  }
  return null;
}

export function setGrid(cfg: Config, profile: string, patch: Partial<Grid>): Config {
  const next = clone(cfg);
  Object.assign(next.profiles[profile].grid, patch);
  return next;
}

export function setTheme(cfg: Config, profile: string, patch: Partial<Theme>): Config {
  const next = clone(cfg);
  Object.assign(next.profiles[profile].theme, patch);
  return next;
}

/** Calls fn on an action and on the actions inside it (a toggle's halves, a multi's steps). */
function walk(a: Action | undefined, fn: (a: Action) => void) {
  if (!a || typeof a !== "object") return;
  fn(a);
  walk(a.on as Action | undefined, fn);
  walk(a.off as Action | undefined, fn);
  if (Array.isArray(a.steps)) a.steps.forEach((s) => walk(s as Action, fn));
}

/** The "page" and "pageName" buttons that open `page`, as [page, cell] pairs. */
export function pageReferences(p: Profile, page: string): [string, string][] {
  const out: [string, string][] = [];
  for (const [pn, pg] of Object.entries(p.pages)) {
    for (const [key, b] of Object.entries(pg.buttons)) {
      walk(b.action, (a) => {
        if (a.type === "page" && a.page === page) out.push([pn, key]);
      });
    }
  }
  return out;
}

export function addPage(cfg: Config, profile: string, name: string): Config {
  const next = clone(cfg);
  next.profiles[profile].pages[name] = { buttons: {} };
  return next;
}

/** Renames a page, and every folder button that opens it. */
export function renamePage(cfg: Config, profile: string, from: string, to: string): Config {
  const next = clone(cfg);
  const p = next.profiles[profile];
  p.pages[to] = p.pages[from];
  delete p.pages[from];
  for (const pg of Object.values(p.pages)) {
    for (const b of Object.values(pg.buttons)) {
      walk(b.action, (a) => {
        if (a.type === "page" && a.page === from) a.page = to;
      });
    }
  }
  return retarget(next, (t) => (t.profile === profile && t.page === from ? { ...t, page: to } : t));
}

/** Why a page cannot be deleted, or null when it can. */
export function deleteBlocker(p: Profile, page: string): string | null {
  if (page === HOME) return `"${HOME}" is the page the deck opens on.`;
  const refs = pageReferences(p, page);
  if (refs.length) return `A folder button still opens it: page "${refs[0][0]}", cell ${refs[0][1]}.`;
  return null;
}

export function deletePage(cfg: Config, profile: string, page: string): Config {
  const next = clone(cfg);
  delete next.profiles[profile].pages[page];
  return retarget(next, (t) => (t.profile === profile && t.page === page ? null : t));
}

/** The page name problem for a new or renamed page, or null. */
export function pageNameProblem(p: Profile, name: string): string | null {
  if (!name.trim()) return "A page needs a name.";
  if (name in p.pages) return `There is already a page "${name}".`;
  return null;
}

/** An undo history of configs. */
export interface History {
  past: Config[];
  present: Config;
  future: Config[];
}
export const start = (c: Config): History => ({ past: [], present: c, future: [] });
export function commit(h: History, c: Config): History {
  if (c === h.present) return h;
  return { past: [...h.past, h.present].slice(-200), present: c, future: [] };
}
export function undo(h: History): History {
  if (!h.past.length) return h;
  return { past: h.past.slice(0, -1), present: h.past[h.past.length - 1], future: [h.present, ...h.future] };
}
export function redo(h: History): History {
  if (!h.future.length) return h;
  return { past: [...h.past, h.present], present: h.future[0], future: h.future.slice(1) };
}

/** The `run` commands in next that saved does not have, as the exact line each would run. */
export function newRunCommands(saved: Config, next: Config): string[] {
  const seen = new Set<string>();
  const collect = (cfg: Config, into: (line: string, key: string) => void) => {
    for (const p of Object.values(cfg.profiles)) {
      for (const pg of Object.values(p.pages)) {
        for (const b of Object.values(pg.buttons)) {
          walk(b.action, (a) => {
            if (a.type !== "run") return;
            const args = Array.isArray(a.args) ? (a.args as string[]) : [];
            const line = a.shell ? `sh: ${a.command}` : [a.command, ...args].map((x) => (/\s/.test(String(x)) ? JSON.stringify(x) : x)).join(" ");
            into(line, JSON.stringify(a));
          });
        }
      }
    }
  };
  collect(saved, (_, key) => seen.add(key));
  const out: string[] = [];
  collect(next, (line, key) => {
    if (!seen.has(key)) {
      seen.add(key);
      out.push(line);
    }
  });
  return out;
}

/** Gives the Show called `name` a profile. Naming the default profile removes the entry, since a
 * Show the config does not name gets it anyway. */
export function setDeviceProfile(cfg: Config, name: string, profile: string): Config {
  const next = clone(cfg);
  next.devices ??= {};
  next.devices[name] = { profile };
  return next;
}

export function forgetDevice(cfg: Config, name: string): Config {
  const next = clone(cfg);
  if (next.devices) delete next.devices[name];
  return next;
}

/** A new profile that starts as a copy of `from`. */
export function addProfile(cfg: Config, name: string, from: string): Config {
  const next = clone(cfg);
  next.profiles[name] = clone(next.profiles[from]);
  return next;
}

export function profileNameProblem(cfg: Config, name: string): string | null {
  if (!name.trim()) return "A profile needs a name.";
  if (name in cfg.profiles) return `There is already a profile "${name}".`;
  return null;
}

/** Devices that use a profile, so it is not deleted from under them. */
export function profileUsers(cfg: Config, profile: string): string[] {
  return Object.entries(cfg.devices ?? {}).filter(([, d]) => d.profile === profile).map(([n]) => n);
}

export function deleteProfile(cfg: Config, profile: string): Config {
  const next = clone(cfg);
  delete next.profiles[profile];
  return retarget(next, (t) => (t.profile === profile ? null : t));
}

/** Why a profile cannot be deleted, or null when it can. */
export function profileDeleteBlocker(cfg: Config, profile: string): string | null {
  if (profile === "default") return `"default" is the profile a Show gets when the config does not name it.`;
  const users = profileUsers(cfg, profile);
  if (users.length) return `${users.join(", ")} uses it. Give ${users.length > 1 ? "them" : "it"} another profile first.`;
  return null;
}

/** What the connection rate of a Show is, from two readings of its totals. */
export function rate(prev: { t: number; bytes: number; frames: number } | undefined, now: { t: number; bytes: number; frames: number }) {
  if (!prev || now.t <= prev.t || now.bytes < prev.bytes) return { kbps: 0, fps: 0 };
  const s = (now.t - prev.t) / 1000;
  return { kbps: ((now.bytes - prev.bytes) * 8) / 1000 / s, fps: (now.frames - prev.frames) / s };
}
