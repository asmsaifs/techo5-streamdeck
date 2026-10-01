// A stand-in for the Wails runtime, so the editor can be opened in an ordinary browser
// ("npm run dev:mock") with a canned config. The Go side is not involved: the preview is a plain
// picture of the grid, and Test and Save only answer.
import example from "./config.json";

let cfg: unknown = structuredClone(example);

const schemas = [
  { type: "page", label: "Folder", group: "Navigation", help: "Opens another page of this deck.", runnable: false, fields: [{ name: "page", label: "Page", kind: "page", required: true }] },
  { type: "back", label: "Back", group: "Navigation", runnable: false, fields: null },
  { type: "stream.web", label: "Show website", group: "Stream", runnable: false, help: "Shows a website on the Show and passes its touches to it.", fields: [{ name: "url", label: "URL", kind: "string", required: true, help: "With a scheme, like https://example.com" }, { name: "allow", label: "Other sites it may use", kind: "list" }, { name: "profile", label: "Browser profile", kind: "string" }] },
  { type: "open.url", label: "Open site", group: "Open", runnable: true, fields: [{ name: "url", label: "URL", kind: "string", required: true, help: "With a scheme, like https://example.com" }] },
  { type: "keys", label: "Hotkey", group: "Keyboard", runnable: true, fields: [{ name: "keys", label: "Keys", kind: "keys", required: true, help: "Press the combination to record it" }] },
  { type: "run", label: "Run command", group: "Scripts", runnable: true, help: "Runs a program with arguments.", fields: [
    { name: "command", label: "Command", kind: "string", required: true },
    { name: "args", label: "Arguments", kind: "list", help: "One per line" },
    { name: "timeout", label: "Timeout (s)", kind: "number", min: 1, max: 600 },
    { name: "shell", label: "Run as a shell line", kind: "bool" } ] },
  { type: "multi", label: "Multi", group: "Composite", runnable: true, fields: [{ name: "steps", label: "Steps", kind: "actions", required: true }] },
  { type: "toggle", label: "Toggle", group: "Composite", runnable: false, fields: [
    { name: "on", label: "When turned on", kind: "action", required: true },
    { name: "off", label: "When turned off", kind: "action", required: true } ] },
  { type: "delay", label: "Delay", group: "Composite", runnable: true, fields: [{ name: "ms", label: "Milliseconds", kind: "number", required: true, min: 0, max: 60000 }] },
];

const icon = (d: string) => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="${d}"/></svg>`;

const handlers: Record<string, (...a: any[]) => unknown> = {
  Settings: () => ({ Listen: "0.0.0.0:9555", Key: "mock-key-0123456789abcdef", Running: true, Address: "192.168.1.20:9555" }),
  Browsers: () => [{ Profile: "youtube-com", Tabs: 2, Parked: 1, Memory: 412000000 }],
  Devices: () => [{ Name: "Kitchen Show", Addr: "192.168.1.31:51234", W: 960, H: 480, Profile: "default", Source: "deck", Since: "", Bytes: Date.now() % 100000 * 40, Frames: Math.floor(Date.now() / 100) % 100000 }],
  SetListen: () => undefined,
  RegenerateKey: () => "mock-key-new",
  Config: () => JSON.stringify(cfg),
  Save: (j: string) => void (cfg = JSON.parse(j)),
  Schemas: () => schemas,
  HotkeyProblems: () => ({}),
  Windows: () => [{ id: "1", app: "Spotify", title: "Spotify Premium", w: 1200, h: 800 }, { id: "2", app: "Notes", title: "Todo", w: 700, h: 500 }],
  FrontApp: () => ({ Name: "Safari", ID: "com.apple.Safari" }),
  Secrets: () => ({ HomeAssistantToken: false, OBSPassword: false }),
  SetSecret: () => undefined,
  TestAction: () => undefined,
  LucideNames: () => Array.from({ length: 300 }, (_, i) => `lucide:icon-${i}`),
  LucideSVGs: (names: string[]) => Object.fromEntries(names.map((n, i) => [n, icon(String(4 + (i % 8)))])),
  UserIcons: () => ({}),
  UploadIcon: (n: string) => n,
  Preview: (j: string, profile: string, page: string, w: number, h: number) => {
    const c = JSON.parse(j);
    const p = c.profiles[profile];
    const rects: string[] = [];
    const gap = p.grid.gap;
    const cw = (w - gap * (p.grid.cols + 1)) / p.grid.cols;
    const ch = (h - gap * (p.grid.rows + 1)) / p.grid.rows;
    for (const [k, b] of Object.entries<any>(p.pages[page].buttons)) {
      const [col, row] = k.split(",").map(Number);
      const x = gap + col * (cw + gap), y = gap + row * (ch + gap);
      rects.push(`<rect x="${x}" y="${y}" width="${cw}" height="${ch}" rx="${p.grid.radius}" fill="${p.theme.button}"/><text x="${x + cw / 2}" y="${y + ch - 14}" fill="${p.theme.text}" font-size="18" text-anchor="middle" font-family="sans-serif">${b.label ?? ""}</text>`);
    }
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}"><rect width="100%" height="100%" fill="${p.theme.bg}"/>${rects.join("")}</svg>`;
    return "data:image/svg+xml;base64," + btoa(svg);
  },
};

export const Call = {
  ByName: async (name: string, ...args: unknown[]) => {
    const fn = handlers[name.split(".").pop()!];
    if (!fn) throw new Error("mock: no " + name);
    return fn(...args);
  },
};
