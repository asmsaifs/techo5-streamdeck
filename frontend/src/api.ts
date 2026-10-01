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

import type { Schema } from "./schema";

export const loadSchemas = () => call<Schema[]>("Schemas");
/** Hotkeys of the config the system refused, by combo, with the reason. */
export const hotkeyProblems = () => call<Record<string, string>>("HotkeyProblems");
export const testAction = (a: unknown) => call<void>("TestAction", JSON.stringify(a));
export const lucideNames = () => call<string[]>("LucideNames");
export const lucideSVGs = (names: string[]) => call<Record<string, string>>("LucideSVGs", names);
export const userIcons = () => call<Record<string, string>>("UserIcons");
export const uploadIcon = (name: string, dataURL: string) => call<string>("UploadIcon", name, dataURL);

export interface Settings {
  Listen: string;
  Key: string;
  Running: boolean;
  Address: string;
}
export interface Device {
  Name: string;
  Addr: string;
  W: number;
  H: number;
  Profile: string;
  Source: string;
  Since: string;
  Bytes: number;
  Frames: number;
}
export const settings = () => call<Settings>("Settings");
export const devices = () => call<Device[]>("Devices");
export const setListen = (addr: string) => call<void>("SetListen", addr);
export const regenerateKey = () => call<string>("RegenerateKey");
