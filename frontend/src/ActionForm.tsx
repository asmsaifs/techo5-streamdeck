import { useState } from "react";
import { comboOf } from "./keys";
import type { Action } from "./model";
import type { Field, Schemas } from "./schema";
import { blankAction } from "./schema";

interface Ctx {
  schemas: Schemas;
  pages: string[]; // the pages of this profile, for a folder's target
}

const MAC = /Mac|iPhone|iPad/.test(navigator.platform);

/** The form for one action: a type picker, and a field per parameter its schema lists. */
export function ActionForm(p: Ctx & { action: Action; onChange: (a: Action) => void; allowNone?: boolean; onNone?: () => void; typeFilter?: (t: string) => boolean }) {
  const s = p.schemas[p.action.type];
  const groups = Object.values(p.schemas).reduce<Record<string, typeof s[]>>((acc, x) => {
    if (!p.typeFilter || p.typeFilter(x.type)) (acc[x.group] ??= []).push(x);
    return acc;
  }, {});
  return (
    <div className="aform">
      <label>
        Action
        <select
          value={p.action.type}
          onChange={(e) => (e.target.value ? p.onChange(blankAction(p.schemas, e.target.value, p.pages)) : p.onNone?.())}
        >
          {p.allowNone && <option value="">(none)</option>}
          {!s && p.action.type && <option value={p.action.type}>{p.action.type} (unknown)</option>}
          {Object.entries(groups).map(([g, list]) => (
            <optgroup key={g} label={g}>
              {list.map((x) => (
                <option key={x.type} value={x.type}>
                  {x.label}
                </option>
              ))}
            </optgroup>
          ))}
        </select>
      </label>
      {s?.help && <p className="hint">{s.help}</p>}
      {!s && <p className="msg err">No action called “{p.action.type}”.</p>}
      {s?.fields?.map((f) => (
        <FieldInput key={f.name} f={f} ctx={p} value={p.action[f.name]} set={(v) => p.onChange(withField(p.action, f.name, v))} />
      ))}
    </div>
  );
}

/** a with one parameter set; an empty value removes it so the file stays short. */
function withField(a: Action, name: string, v: unknown): Action {
  const next = { ...a };
  if (v === undefined || v === "" || v === false || (Array.isArray(v) && v.length === 0 && name !== "steps")) delete next[name];
  else next[name] = v;
  return next;
}

function FieldInput({ f, ctx, value, set }: { f: Field; ctx: Ctx; value: unknown; set: (v: unknown) => void }) {
  const label = (
    <span>
      {f.label}
      {f.required && <b className="req"> *</b>}
    </span>
  );
  const miss = f.required && (value === undefined || value === "" || (Array.isArray(value) && !value.length));
  const help = f.help && <small className="hint">{f.help}</small>;
  switch (f.kind) {
    case "bool":
      return (
        <label className="check">
          <input type="checkbox" checked={!!value} onChange={(e) => set(e.target.checked)} />
          <span>
            {label}
            {help}
          </span>
        </label>
      );
    case "text":
      return (
        <label>
          {label}
          <textarea className={miss ? "bad" : ""} rows={3} value={String(value ?? "")} onChange={(e) => set(e.target.value)} />
          {help}
        </label>
      );
    case "number":
      return (
        <label>
          {label}
          <input
            type="number"
            className={miss ? "bad" : ""}
            min={f.min}
            max={f.max}
            value={value === undefined ? "" : String(value)}
            onChange={(e) => set(e.target.value === "" ? undefined : Number(e.target.value))}
          />
          {help}
        </label>
      );
    case "enum":
      return (
        <label>
          {label}
          <select value={String(value ?? "")} onChange={(e) => set(e.target.value)}>
            {!f.required && <option value="" />}
            {f.enum?.map((x) => (
              <option key={x}>{x}</option>
            ))}
          </select>
          {help}
        </label>
      );
    case "page":
      return (
        <label>
          {label}
          <select className={miss ? "bad" : ""} value={String(value ?? "")} onChange={(e) => set(e.target.value)}>
            {!value && <option value="" />}
            {ctx.pages.map((x) => (
              <option key={x}>{x}</option>
            ))}
          </select>
          {help}
        </label>
      );
    case "keys":
      return <KeysInput label={label} help={help} miss={!!miss} value={String(value ?? "")} set={set} />;
    case "list":
      return (
        <label>
          {label}
          <textarea
            rows={3}
            spellCheck={false}
            value={Array.isArray(value) ? value.join("\n") : ""}
            onChange={(e) => set(e.target.value === "" ? undefined : e.target.value.split("\n"))}
          />
          {help}
        </label>
      );
    case "action":
      return (
        <fieldset>
          <legend>{label}</legend>
          <ActionForm {...ctx} action={(value as Action) ?? { type: "delay", ms: 0 }} onChange={set} typeFilter={(t) => t !== "toggle" && t !== "page" && t !== "back"} />
        </fieldset>
      );
    case "actions": {
      const steps = Array.isArray(value) ? (value as Action[]) : [];
      const put = (i: number, a: Action) => set(steps.map((x, j) => (j === i ? a : x)));
      const move = (i: number, d: number) => {
        const n = [...steps];
        [n[i], n[i + d]] = [n[i + d], n[i]];
        set(n);
      };
      return (
        <fieldset>
          <legend>{label}</legend>
          {steps.map((a, i) => (
            <div key={i} className="step">
              <div className="row">
                <b>Step {i + 1}</b>
                <span className="spacer" />
                <button disabled={i === 0} onClick={() => move(i, -1)}>↑</button>
                <button disabled={i === steps.length - 1} onClick={() => move(i, 1)}>↓</button>
                <button onClick={() => set(steps.filter((_, j) => j !== i))}>✕</button>
              </div>
              <ActionForm {...ctx} action={a} onChange={(x) => put(i, x)} typeFilter={(t) => t !== "toggle" && t !== "page" && t !== "back"} />
            </div>
          ))}
          <button onClick={() => set([...steps, { type: "delay", ms: 0 }])}>Add step</button>
        </fieldset>
      );
    }
    default:
      // string and path
      return (
        <label>
          {label}
          <input className={miss ? "bad" : ""} spellCheck={false} value={String(value ?? "")} onChange={(e) => set(e.target.value)} />
          {help}
        </label>
      );
  }
}

/** A box that records the combination pressed in it, and can still be typed into by hand. */
function KeysInput(p: { label: React.ReactNode; help: React.ReactNode; miss: boolean; value: string; set: (v: unknown) => void }) {
  const [rec, setRec] = useState(false);
  return (
    <label>
      {p.label}
      <div className="row">
        <input
          className={p.miss ? "bad" : ""}
          spellCheck={false}
          value={rec ? "Press the keys…" : p.value}
          readOnly={rec}
          onChange={(e) => p.set(e.target.value)}
          onKeyDown={(e) => {
            if (!rec) return;
            e.preventDefault();
            e.stopPropagation();
            if (e.key === "Escape") return setRec(false);
            const c = comboOf(e.nativeEvent, MAC);
            if (c) (p.set(c), setRec(false));
          }}
          onBlur={() => setRec(false)}
        />
        <button type="button" onMouseDown={(e) => e.preventDefault()} onClick={(e) => (setRec(true), (e.currentTarget.previousSibling as HTMLInputElement).focus())}>
          Record
        </button>
      </div>
      {p.help}
    </label>
  );
}
