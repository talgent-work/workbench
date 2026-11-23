import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { WorkHistory, type WorkHistorySource } from "../components/work/work-history";

vi.mock("../lib/client", () => ({ agentWorkClient: {} }));

const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;
const source = (rows: WorkHistorySource["data"]): WorkHistorySource => ({ data: rows, isLoading: false, error: null, refetch: () => undefined, affects: [] });

it("switches between Recent and Archived, which the entry owns", async () => {
  const onViewChange = vi.fn();
  render(<WorkHistory history={source([{ id: "w1", title: "Open work", state: "stopped", waiting: false, to: "/works/w1" }])} view="recent" onViewChange={onViewChange} activeId={null} onNavigate={() => undefined} />, { wrapper });

  expect(screen.getByRole("tab", { name: "Recent" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByRole("listbox", { name: "Recent" })).toHaveTextContent("Open work");
  await userEvent.click(screen.getByRole("tab", { name: "Archived" }));
  expect(onViewChange).toHaveBeenCalledWith("archived");
});

it("says when nothing is archived", () => {
  render(<WorkHistory history={source([])} view="archived" onViewChange={() => undefined} activeId={null} onNavigate={() => undefined} />, { wrapper });

  expect(screen.getByRole("tab", { name: "Archived" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByRole("listbox", { name: "Archived" })).toHaveTextContent("No archived work");
});
