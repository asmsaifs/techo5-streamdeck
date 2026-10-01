import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The Go app embeds dist/, so the paths must be relative to wherever the asset server mounts it.
export default defineConfig({ plugins: [react()], base: "./" });
