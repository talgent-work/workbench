import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import { ClientSecretState } from "@talgent/contracts/account/public/v1/application_pb";
import { EntryShellContext, personalPathsAt, type PageLayoutProps } from "../components/shell/entry-shell";
import { applicationNameProblem } from "../lib/application-service";
import { ApplicationsPage } from "../pages/applications/applications-page";

const userId = "01a11fb2-e6c2-701f-80b7-38c54aa2eae9";
const deskId = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e";
const ciId = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4f";
const secret = "wbs_" + "A".repeat(39) + "k4Qz";

type Stored = { id: string; name: string; registeredAtUnixMs: bigint; clientSecrets: Array<Record<string, unknown>> };
const stored: Stored[] = [];
const applicationOf = (id: string, name: string, lastUsed = 0n): Stored => ({
  id, name, registeredAtUnixMs: BigInt(Date.now()),
  clientSecrets: [{ id: `${id}-s`, state: ClientSecretState.CURRENT, hint: "k4Qz", issuedAtUnixMs: BigInt(Date.now()), lastUsedAtUnixMs: lastUsed, overlapEndsAtUnixMs: 0n }],
});

vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: userId } } }) }));
vi.mock("../lib/client", () => ({
  applicationClient: {
    listOwnedApplications: async () => ({ applications: [...stored] }),
    registerApplication: async ({ name }: { name: string }) => {
      const application = applicationOf(deskId, name);
      stored.push(application);
      return { application, clientSecret: secret };
    },
  },
}));

beforeEach(() => { stored.length = 0; });

// The frame a page renders in: its lead, tabs and crumbs, as an entry draws them.
function Layout({ children, lead, centerActions, trail = [], workspaceTitle }: PageLayoutProps) {
  return <div>
    <header>{lead}<nav aria-label="Page location">{trail.map(crumb => <span key={crumb.label}>{crumb.label}</span>)}{!lead && workspaceTitle}</nav>{centerActions}</header>
    <main>{children}</main>
  </div>;
}

function renderPage(path = "/applications") {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>
    <MemoryRouter initialEntries={[path]}><EntryShellContext value={{ Layout, home: "/", personal: personalPathsAt("") }}>{children}</EntryShellContext></MemoryRouter>
  </QueryClientProvider>;
  return render(<ApplicationsPage />, { wrapper });
}

it("registers a first application and shows its secret once, until the owner says they stored it", async () => {
  const user = userEvent.setup();
  renderPage();

  await user.click(await screen.findByRole("button", { name: "Register application" }));
  const dialog = await screen.findByRole("dialog");
  await user.click(within(dialog).getByRole("button", { name: "Register" }));
  expect(within(dialog).getByText("Enter a name.")).toBeInTheDocument();
  await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Support Desk");
  await user.click(within(dialog).getByRole("button", { name: "Register" }));

  expect(await within(dialog).findByRole("heading", { name: "Support Desk is registered" })).toBeInTheDocument();
  expect(within(dialog).getByText(deskId)).toBeInTheDocument();
  expect(within(dialog).getByText(secret)).toBeInTheDocument();
  const done = within(dialog).getByRole("button", { name: "Done" });
  expect(done).toBeDisabled();
  await user.click(within(dialog).getByRole("checkbox", { name: "I stored the secret" }));
  await user.click(done);

  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  expect(screen.queryByText(secret)).not.toBeInTheDocument();
  const credentials = await screen.findByRole("list", { name: "Support Desk credentials" });
  expect(within(credentials).getByText(deskId)).toBeInTheDocument();
  expect(within(credentials).getByText("wbs_…k4Qz")).toBeInTheDocument();
  expect(within(credentials).getByText(/never used/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Application menu: Support Desk" })).toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "Credentials" })).toHaveAttribute("aria-selected", "true");
});

it("lists the owner's applications in the switcher and opens the one in ?app=", async () => {
  const user = userEvent.setup();
  stored.push(applicationOf(deskId, "Support Desk"), applicationOf(ciId, "CI Triage", BigInt(Date.now() - 2 * 3_600_000)));
  renderPage(`/applications?app=${ciId}`);

  const credentials = await screen.findByRole("list", { name: "CI Triage credentials" });
  expect(within(credentials).getByText(/last used 2h ago/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Application menu: CI Triage" }));
  const menu = await screen.findByRole("menu");
  expect(within(menu).getAllByRole("menuitem").map(item => item.textContent)).toEqual(["Support Desk", "CI Triage", "Register application"]);
  await user.click(within(menu).getByRole("menuitem", { name: "Support Desk" }));
  expect(await screen.findByRole("list", { name: "Support Desk credentials" })).toBeInTheDocument();
});

it("explains an empty list and invites a registration", async () => {
  renderPage();
  expect(await screen.findByText("No applications yet")).toBeInTheDocument();
  expect(screen.queryByRole("tab")).not.toBeInTheDocument();
});

it("bounds a name to 60 characters after trimming", () => {
  expect(applicationNameProblem("   ")).toBe("Enter a name.");
  expect(applicationNameProblem(` ${"x".repeat(60)} `)).toBe("");
  expect(applicationNameProblem("x".repeat(61))).toBe("Use at most 60 characters.");
});
