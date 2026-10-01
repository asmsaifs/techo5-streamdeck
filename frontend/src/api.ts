// The Go side, reached by name so no binding generator is needed. Errors come back as the text
// Go wrote, which names the config path at fault.
import { Call } from "@wailsio/runtime";
import type { Config } from "./model";

const call = <T,>(method: string, ...args: unknown[]) => Call.ByName(`main.Editor.${method}`, ...args) as Promise<T>;

export async function loadConfig(): Promise<Config> {
  return JSON.parse(await call<string>("Config"));
}
export const saveConfig = (c: Config) => call<void>("Save", JSON.stringify(c));
export const preview = (c: Config, profile: string, page: string, w: number, h: number) =>
  call<string>("Preview", JSON.stringify(c), profile, page, w, h);

export const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e));
