// What the Go side says an action looks like (internal/actions/schema.go), and the checks the
// editor can do from it before anything is saved.
import type { Action, Config } from "./model";

export interface Field {
  name: string;
  label: string;
  kind: "string" | "text" | "number" | "bool" | "tribool" | "enum" | "path" | "keys" | "list" | "page" | "action" | "actions";
  required?: boolean;
  enum?: string[];
  help?: string;
  min?: number;
  max?: number;
}
export interface Schema {
  type: string;
  label: string;
  group: string;
  help?: string;
  fields: Field[] | null;
  runnable: boolean;
}
export type Schemas = Record<string, Schema>;

export const bySchemaType = (list: Schema[]): Schemas => Object.fromEntries(list.map((s) => [s.type, s]));

/** A fresh action of a type, with its nested actions filled in so the form has something to show. */
export function blankAction(schemas: Schemas, type: string, pages: string[] = []): Action {
  const a: Action = { type };
  for (const f of schemas[type]?.fields ?? []) {
    if (f.kind === "page" && pages[0]) a[f.name] = pages[0];
    else if (f.kind === "action") a[f.name] = { type: "delay", ms: 0 };
    else if (f.kind === "actions") a[f.name] = [{ type: "delay", ms: 0 }];
    else if (f.kind === "number" && f.required) a[f.name] = f.min ?? 0;
    else if (f.kind === "bool") a[f.name] = false;
  }
  return a;
}

const empty = (v: unknown) => v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0);

/** What is wrong with an action's parameters, one line per problem, with the field's label. */
export function actionProblems(schemas: Schemas, a: Action, where = ""): string[] {
  const s = schemas[a.type];
  if (!s) return [`${where}there is no action "${a.type}"`];
  const out: string[] = [];
  for (const f of s.fields ?? []) {
    const v = a[f.name];
    if (f.required && empty(v)) out.push(`${where}${s.label}: ${f.label} is missing`);
    if (f.kind === "number" && typeof v === "number") {
      if (f.min !== undefined && v < f.min) out.push(`${where}${s.label}: ${f.label} must be at least ${f.min}`);
      if (f.max !== undefined && v > f.max) out.push(`${where}${s.label}: ${f.label} must be at most ${f.max}`);
    }
    if (f.kind === "action" && v) out.push(...actionProblems(schemas, v as Action, `${where}${f.label} → `));
    if (f.kind === "actions" && Array.isArray(v)) v.forEach((x, i) => out.push(...actionProblems(schemas, x as Action, `${where}step ${i + 1} → `)));
  }
  return out;
}

/** The first problem in any button of the config, as "page home, cell 1,0: ...", or null. */
export function firstProblem(schemas: Schemas, cfg: Config): string | null {
  for (const [pn, p] of Object.entries(cfg.profiles)) {
    for (const [gn, pg] of Object.entries(p.pages)) {
      for (const [key, b] of Object.entries(pg.buttons)) {
        if (!b.action) continue;
        const [first] = actionProblems(schemas, b.action);
        if (first) return `Profile ${pn}, page ${gn}, cell ${key}: ${first}`;
      }
    }
  }
  return null;
}
