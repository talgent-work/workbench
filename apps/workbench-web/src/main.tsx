import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./app.css";
import App from "./app";
import { installStaleBuildReload } from "@talgent/workbench-ui/lib/stale-build-reload";

installStaleBuildReload();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>
);
