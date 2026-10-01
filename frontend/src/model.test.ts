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

describe("profiles and devices", () => {
  it("copies, guards and deletes profiles", () => {
    const c = m.setDeviceProfile(base(), "Kitchen", "kids");
    const withKids = m.addProfile(c, "kids", "default");
    expect(Object.keys(withKids.profiles)).toEqual(["default", "kids"]);
    withKids.profiles.kids.grid.cols = 2;
    expect(withKids.profiles.default.grid.cols).toBe(5); // a copy, not the same object
    expect(m.profileDeleteBlocker(withKids, "default")).toMatch(/default/);
    expect(m.profileDeleteBlocker(withKids, "kids")).toMatch(/Kitchen/);
    expect(m.profileDeleteBlocker(m.forgetDevice(withKids, "Kitchen"), "kids")).toBeNull();
    expect(m.profileNameProblem(withKids, "kids")).not.toBeNull();
    expect(m.profileNameProblem(withKids, "new")).toBeNull();
  });

  it("turns two readings into a rate, and survives a reconnect", () => {
    const a = { t: 0, bytes: 0, frames: 0 };
    expect(m.rate(a, { t: 2000, bytes: 250_000, frames: 60 })).toEqual({ kbps: 1000, fps: 30 });
    expect(m.rate(undefined, a)).toEqual({ kbps: 0, fps: 0 });
    expect(m.rate({ t: 0, bytes: 900, frames: 9 }, { t: 1000, bytes: 10, frames: 1 })).toEqual({ kbps: 0, fps: 0 });
  });
});

describe("global hotkeys", () => {
  const withKey = () => m.setHotkey(base(), "default", "home", "1,0", "CmdOrCtrl+Alt+1");

  it("gives a button one hotkey, and a combo one button", () => {
    const c = withKey();
    expect(m.hotkeyOf(c, "default", "home", "1,0")).toBe("CmdOrCtrl+Alt+1");
    // Another combo replaces the first; the same combo on another button moves it there.
    const again = m.setHotkey(c, "default", "home", "1,0", "Alt+2");
    expect(Object.keys(again.hotkeys!)).toEqual(["Alt+2"]);
    const stolen = m.setHotkey(c, "default", "home", "0,0", "CmdOrCtrl+Alt+1");
    expect(m.hotkeyOf(stolen, "default", "home", "1,0")).toBeUndefined();
    expect(m.hotkeyOf(stolen, "default", "home", "0,0")).toBe("CmdOrCtrl+Alt+1");
    expect(m.hotkeyTakenBy(c, "CmdOrCtrl+Alt+1", "default", "home", "0,0")).toBe("default / home / 1,0");
    expect(m.hotkeyTakenBy(c, "CmdOrCtrl+Alt+1", "default", "home", "1,0")).toBeNull();
    expect(base().hotkeys).toBeUndefined();
  });

  it("removing the hotkey leaves no empty section", () => {
    expect(m.setHotkey(withKey(), "default", "home", "1,0", null).hotkeys).toBeUndefined();
  });

  it("follows its button when moved or swapped, and goes when the button does", () => {
    const moved = m.moveButton(withKey(), "default", "home", "1,0", "2,2");
    expect(moved.hotkeys!["CmdOrCtrl+Alt+1"].button).toBe("2,2");
    const swapped = m.moveButton(withKey(), "default", "home", "0,0", "1,0");
    expect(swapped.hotkeys!["CmdOrCtrl+Alt+1"].button).toBe("0,0");
    expect(m.setButton(withKey(), "default", "home", "1,0", null).hotkeys).toBeUndefined();
  });

  it("follows a renamed page and goes with a deleted page or profile", () => {
    const c = m.setHotkey(base(), "default", "apps", "0,0", "Alt+9");
    expect(m.renamePage(c, "default", "apps", "tools").hotkeys!["Alt+9"].page).toBe("tools");
    expect(m.deletePage(c, "default", "apps").hotkeys).toBeUndefined();
    const two = m.addProfile(c, "kids", "default");
    expect(m.deleteProfile(m.setHotkey(two, "kids", "home", "1,0", "Alt+8"), "kids").hotkeys!["Alt+9"]).toBeDefined();
    expect(m.deleteProfile(m.setHotkey(two, "kids", "home", "1,0", "Alt+8"), "kids").hotkeys!["Alt+8"]).toBeUndefined();
  });
});

describe("integrations", () => {
  it("sets and clears an address, and drops the bare section", () => {
    const a = m.setIntegration(base(), "obs", " localhost:4455 ");
    expect(a.integrations).toEqual({ obs: "localhost:4455" });
    const b = m.setIntegration(a, "homeassistant", "http://ha:8123");
    expect(b.integrations).toEqual({ obs: "localhost:4455", homeassistant: "http://ha:8123" });
    expect(m.setIntegration(m.setIntegration(b, "obs", ""), "homeassistant", " ").integrations).toBeUndefined();
    expect(base().integrations).toBeUndefined();
  });
});

describe("live tiles", () => {
  it("sets and removes a tile on a button", () => {
    const b = m.setTile({ label: "CPU" }, { type: "cpu" });
    expect(b).toEqual({ label: "CPU", tile: { type: "cpu" } });
    expect(m.setTile(b, null)).toEqual({ label: "CPU" });
  });
  it("says what a tile still needs", () => {
    expect(m.tileProblem({ type: "clock" })).toBeNull();
    expect(m.tileProblem({ type: "ha_state", entity: "Sensor X" })).toMatch(/Entity/);
    expect(m.tileProblem({ type: "ha_state", entity: "sensor.x" })).toBeNull();
    expect(m.tileProblem({ type: "script" })).toMatch(/command/);
    expect(m.tileProblem({ type: "state", command: "x" })).toBeNull();
    expect(m.tileProblem({ type: "cpu", every: 0.5 })).toMatch(/1 to 3600/);
  });
});

describe("auto-switch rules", () => {
  it("sets rules, drops the bare section, and drops rules of a deleted profile", () => {
    const c = m.addProfile(base(), "game", "default");
    const a = m.setAutoSwitch(c, [{ app: "Steam", profile: "game", device: "" }, { app: "Zoom", profile: "default" }]);
    expect(a.autoSwitch).toEqual([{ app: "Steam", profile: "game" }, { app: "Zoom", profile: "default" }]);
    expect(m.deleteProfile(a, "game").autoSwitch).toEqual([{ app: "Zoom", profile: "default" }]);
    expect(m.setAutoSwitch(a, []).autoSwitch).toBeUndefined();
    expect(m.deleteProfile(m.setAutoSwitch(c, [{ app: "Steam", profile: "game" }]), "game").autoSwitch).toBeUndefined();
  });
  it("says what a rule needs", () => {
    const c = base();
    expect(m.ruleProblem(c, { app: " ", profile: "default" })).toMatch(/application/);
    expect(m.ruleProblem(c, { app: "x", profile: "nope" })).toMatch(/profile/);
    expect(m.ruleProblem(c, { app: "x", profile: "default" })).toBeNull();
  });
});
