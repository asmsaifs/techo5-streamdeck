import { describe, expect, it } from "vitest";
import * as m from "./model";

const base = (): m.Config => ({
  version: 1,
  server: { listen: "", key: "k" },
  profiles: {
    default: {
      grid: { cols: 5, rows: 3, gap: 8, radius: 14 },
      theme: { bg: "#000", button: "#111", text: "#fff", accent: "#00f" },
      pages: {
        home: { buttons: { "0,0": { label: "A", action: { type: "page", page: "apps" } }, "1,0": { label: "B" } } },
        apps: { buttons: {} },
      },
    },
  },
});

describe("model", () => {
  it("moves into an empty cell and swaps into a full one, without touching the input", () => {
    const c = base();
    const moved = m.moveButton(c, "default", "home", "1,0", "2,2");
    expect(moved.profiles.default.pages.home.buttons["2,2"].label).toBe("B");
    expect("1,0" in moved.profiles.default.pages.home.buttons).toBe(false);
    const swapped = m.moveButton(c, "default", "home", "0,0", "1,0");
    expect(swapped.profiles.default.pages.home.buttons["0,0"].label).toBe("B");
    expect(swapped.profiles.default.pages.home.buttons["1,0"].label).toBe("A");
    expect(c.profiles.default.pages.home.buttons["0,0"].label).toBe("A");
  });

  it("renames a page and the folder buttons that open it, even inside a toggle", () => {
    const c = base();
    c.profiles.default.pages.home.buttons["3,0"] = {
      action: { type: "toggle", on: { type: "page", page: "apps" }, off: { type: "back" } },
    };
    const r = m.renamePage(c, "default", "apps", "tools");
    const home = r.profiles.default.pages.home.buttons;
    expect(home["0,0"].action?.page).toBe("tools");
    expect((home["3,0"].action?.on as m.Action).page).toBe("tools");
    expect("apps" in r.profiles.default.pages).toBe(false);
  });

  it("will not delete home or a page a folder opens", () => {
    const p = base().profiles.default;
    expect(m.deleteBlocker(p, "home")).toMatch(/opens on/);
    expect(m.deleteBlocker(p, "apps")).toMatch(/0,0/);
    delete p.pages.home.buttons["0,0"];
    expect(m.deleteBlocker(p, "apps")).toBeNull();
  });

  it("will not shrink the grid under a button", () => {
    const p = base().profiles.default;
    expect(m.shrinkBlocker(p, 1, 3)).toMatch(/1,0/);
    expect(m.shrinkBlocker(p, 2, 1)).toBeNull();
  });

  it("undoes and redoes, and a redo is lost by a new edit", () => {
    const a = base();
    const b = m.setGrid(a, "default", { cols: 6 });
    const c = m.setGrid(b, "default", { cols: 4 });
    let h = m.commit(m.commit(m.start(a), b), c);
    h = m.undo(m.undo(h));
    expect(h.present).toBe(a);
    h = m.redo(h);
    expect(h.present).toBe(b);
    h = m.commit(h, m.setGrid(b, "default", { rows: 2 }));
    expect(h.future).toHaveLength(0);
    expect(m.undo(m.start(a)).present).toBe(a);
  });

  it("checks page names", () => {
    const p = base().profiles.default;
    expect(m.pageNameProblem(p, " ")).not.toBeNull();
    expect(m.pageNameProblem(p, "apps")).not.toBeNull();
    expect(m.pageNameProblem(p, "new")).toBeNull();
  });
});

describe("run confirmation", () => {
  it("lists only the commands the saved config does not already have", () => {
    const saved = base();
    saved.profiles.default.pages.home.buttons["2,0"] = { action: { type: "run", command: "say", args: ["hello"] } };
    const next = structuredClone(saved);
    next.profiles.default.pages.home.buttons["3,0"] = { action: { type: "run", command: "open", args: ["My File.txt"] } };
    next.profiles.default.pages.apps.buttons["0,0"] = {
      action: { type: "toggle", on: { type: "run", command: "ls | wc", shell: true }, off: { type: "delay", ms: 1 } },
    };
    expect(m.newRunCommands(saved, next)).toEqual(['open "My File.txt"', "sh: ls | wc"]);
    expect(m.newRunCommands(next, next)).toEqual([]);
  });
});
