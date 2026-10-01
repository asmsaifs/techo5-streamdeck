import { describe, expect, it } from "vitest";
import { comboOf } from "./keys";
import type { Config } from "./model";
import { actionProblems, blankAction, bySchemaType, firstProblem } from "./schema";

const schemas = bySchemaType([
  { type: "open.url", label: "Open site", group: "Open", runnable: true, fields: [{ name: "url", label: "URL", kind: "string", required: true }] },
  { type: "delay", label: "Delay", group: "Composite", runnable: true, fields: [{ name: "ms", label: "Milliseconds", kind: "number", required: true, min: 0, max: 60000 }] },
  { type: "toggle", label: "Toggle", group: "Composite", runnable: false, fields: [
    { name: "on", label: "When turned on", kind: "action", required: true },
    { name: "off", label: "When turned off", kind: "action", required: true } ] },
  { type: "multi", label: "Multi", group: "Composite", runnable: true, fields: [{ name: "steps", label: "Steps", kind: "actions", required: true }] },
  { type: "back", label: "Back", group: "Navigation", runnable: false, fields: null },
]);

describe("schema checks", () => {
  it("names a missing field, a number out of range, and a problem inside a nested action", () => {
    expect(actionProblems(schemas, { type: "open.url", url: "" })).toEqual(["Open site: URL is missing"]);
    expect(actionProblems(schemas, { type: "delay", ms: 99999 })).toHaveLength(1);
    expect(actionProblems(schemas, { type: "delay", ms: 0 })).toEqual([]);
    expect(actionProblems(schemas, { type: "toggle", on: { type: "open.url" }, off: { type: "delay", ms: 1 } })).toEqual([
      "When turned on → Open site: URL is missing",
    ]);
    expect(actionProblems(schemas, { type: "multi", steps: [{ type: "delay", ms: 0 }, { type: "open.url" }] })[0]).toMatch(/^step 2/);
    expect(actionProblems(schemas, { type: "back" })).toEqual([]);
    expect(actionProblems(schemas, { type: "zzz" })).toHaveLength(1);
  });

  it("makes blank actions the form can show", () => {
    expect(blankAction(schemas, "toggle")).toEqual({ type: "toggle", on: { type: "delay", ms: 0 }, off: { type: "delay", ms: 0 } });
    expect(blankAction(schemas, "multi").steps).toHaveLength(1);
  });

  it("finds the first bad button in a config", () => {
    const cfg = { profiles: { default: { pages: { home: { buttons: { "1,0": { action: { type: "open.url" } } } } } } } } as unknown as Config;
    expect(firstProblem(schemas, cfg)).toBe("Profile default, page home, cell 1,0: Open site: URL is missing");
  });
});

describe("key recorder", () => {
  const e = (code: string, mods: Partial<Record<"metaKey" | "ctrlKey" | "altKey" | "shiftKey", boolean>> = {}) => ({
    code, metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, ...mods,
  });
  it("writes the main modifier as CmdOrCtrl", () => {
    expect(comboOf(e("Digit4", { metaKey: true, shiftKey: true }), true)).toBe("CmdOrCtrl+Shift+4");
    expect(comboOf(e("Digit4", { ctrlKey: true, shiftKey: true }), false)).toBe("CmdOrCtrl+Shift+4");
    expect(comboOf(e("KeyC", { ctrlKey: true }), true)).toBe("Ctrl+c");
    expect(comboOf(e("Space", { metaKey: true }), false)).toBe("Cmd+Space");
    expect(comboOf(e("F5"), true)).toBe("F5");
  });
  it("ignores a modifier pressed alone", () => {
    expect(comboOf(e("ShiftLeft", { shiftKey: true }), true)).toBeNull();
  });
});
