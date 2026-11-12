import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { PluginPreparationStatus } from "@talgent/contracts/agent/public/v1/plugin_pb";
import { WorkPluginsSection } from "../components/work/work-plugins-section";

const { SUCCEEDED, FAILED, UNSPECIFIED } = PluginPreparationStatus;
vi.mock("../lib/client", () => ({
  agentWorkClient: {
    getWorkPluginResults: async () => ({ plugins: [
      { slug: "talgent", releaseVersion: "1.2.0", loadedVersion: "1.3.0", installation: SUCCEEDED, loading: SUCCEEDED },
      { slug: "code-review", releaseVersion: "2.0.0", loadedVersion: "", installation: FAILED, loading: UNSPECIFIED },
      { slug: "fresh-plugin", releaseVersion: "0.1.0", loadedVersion: "", installation: UNSPECIFIED, loading: UNSPECIFIED },
    ] }),
  },
}));

const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;

it("lists the version each Plugin's Harness loaded, which may be newer than the release taken up", async () => {
  render(<WorkPluginsSection workId="work-1" />, { wrapper });

  expect(await screen.findByRole("button", { name: "Plugins" })).toHaveAccessibleDescription("1 of 3 loaded");
  const rows = within(screen.getByRole("list", { name: "Work plugins", hidden: true })).getAllByRole("listitem", { hidden: true });
  expect(rows.map(row => row.textContent)).toEqual(["Talgent1.3.0", "Code ReviewNot loaded", "Fresh Plugin"]);
});
