import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The Go app embeds dist/, so the paths must be relative to wherever the asset server mounts it.
// "--mode mock" swaps the Wails runtime for a canned one (src/mock), to work on the look in a browser.
export default defineConfig(({ mode }) => ({
  plugins: [react()],
  base: "./",
  resolve: mode === "mock" ? { alias: { "@wailsio/runtime": new URL("./src/mock/wails.ts", import.meta.url).pathname } } : {},
  build: mode === "mock" ? { outDir: "dist-mock" } : {},
}));
