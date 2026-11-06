/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { build, coreProxy, publicDir, test } from "@talgent-work/workbench-ui/vite-preset";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  publicDir,
  build: build(),
  test,
  server: { port: 3000, proxy: coreProxy },
});
