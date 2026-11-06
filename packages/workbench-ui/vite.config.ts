/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { test } from "./vite-preset";

// workbench-ui is consumed as source by the web apps; this config only runs its tests.
export default defineConfig({ plugins: [react()], test });
