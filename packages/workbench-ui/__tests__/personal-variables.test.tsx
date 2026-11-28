import { Code, ConnectError } from "@connectrpc/connect";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ToastRegion } from "../components/feedback/toast-region";
import { PersonalVariables } from "../components/profile/personal-variables";
import { notificationQueue } from "../lib/notifications";
import { recorder, resetServices, services } from "../test-support/backend";

vi.mock("../lib/transport", async () => (await import("../test-support/backend")).transportModule);

// The signed-in person's saved variables, as the core keeps them.
let saved: Map<string, string>;

function keepVariables(initial: Record<string, string>) {
  saved = new Map(Object.entries(initial));
  Object.assign(services.personalVariable, {
    listPersonalVariables: () => ({ variables: [...saved.keys()].sort().map(name => ({ name })) }),
    revealPersonalVariable: ({ name }: { name: string }) => {
      if (!saved.has(name)) throw new ConnectError("not found", Code.NotFound);
      return { value: saved.get(name) };
    },
    setPersonalVariable: recorder(({ name, value }: { name: string; value: string }) => { saved.set(name, value); return { variable: { name } }; }),
    deletePersonalVariable: recorder(({ name }: { name: string }) => { saved.delete(name); return {}; }),
  });
}

function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={client}><PersonalVariables /><ToastRegion /></QueryClientProvider>);
}

const rows = async () => within(await screen.findByRole("list", { name: "Variables" })).getAllByRole("listitem");
const row = async (name: string) => (await rows()).find(item => item.textContent?.includes(name))!;

beforeEach(() => resetServices());
// Notices outlive a render; a danger one stays until dismissed.
afterEach(() => {
  cleanup();
  for (const toast of notificationQueue.visibleToasts) notificationQueue.close(toast.key);
});

it("lists variables by name with every value masked", async () => {
  keepVariables({ OPENAI_API_KEY: "sk-secret", DEPLOY_HOSTS: "a\nb" });
  renderSection();

  const listed = await rows();
  expect(listed.map(item => item.querySelector("code")?.textContent)).toEqual(["DEPLOY_HOSTS", "OPENAI_API_KEY"]);
  expect(screen.queryByText("sk-secret")).not.toBeInTheDocument();
  expect(within(listed[0]).getByText("Value hidden")).toBeInTheDocument();
});

it("offers adding the first variable when the person keeps none", async () => {
  keepVariables({});
  renderSection();

  expect(await screen.findByText("No variables yet.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Add variable" })).toBeInTheDocument();
});

it("adds a variable through an inline row and lists it", async () => {
  const user = userEvent.setup();
  keepVariables({ OPENAI_API_KEY: "sk-secret" });
  renderSection();

  await user.click(await screen.findByRole("button", { name: "Add variable" }));
  const form = screen.getByRole("form", { name: "New variable" });
  await user.type(within(form).getByRole("textbox", { name: "Name" }), "ANTHROPIC_BASE_URL");
  await user.type(within(form).getByRole("textbox", { name: "Value" }), "https://models.example.test");
  await user.click(within(form).getByRole("button", { name: "Save" }));

  expect(await screen.findByRole("button", { name: "Edit ANTHROPIC_BASE_URL" })).toBeInTheDocument();
  expect(services.personalVariable.setPersonalVariable).toHaveBeenCalledOnce();
  expect(saved.get("ANTHROPIC_BASE_URL")).toBe("https://models.example.test");
  expect((await rows()).map(item => item.querySelector("code")?.textContent)).toEqual(["ANTHROPIC_BASE_URL", "OPENAI_API_KEY"]);
  expect(screen.getByRole("button", { name: "Add variable" })).toHaveFocus();
});

it.each([
  ["1ST_KEY", "Use letters, digits and underscores, not starting with a digit."],
  ["TALGENT_WORK_ROOT", "Names starting with TALGENT_, in any letter case, are reserved for the platform."],
  ["talgent_work_root", "Names starting with TALGENT_, in any letter case, are reserved for the platform."],
  ["OPENAI_API_KEY", "OPENAI_API_KEY already exists."],
])("refuses the name %s before sending it", async (name, reason) => {
  const user = userEvent.setup();
  keepVariables({ OPENAI_API_KEY: "sk-secret" });
  renderSection();

  await user.click(await screen.findByRole("button", { name: "Add variable" }));
  const form = screen.getByRole("form", { name: "New variable" });
  await user.type(within(form).getByRole("textbox", { name: "Name" }), name);
  await user.click(within(form).getByRole("button", { name: "Save" }));

  expect(within(form).getByText(reason)).toBeInTheDocument();
  expect(services.personalVariable.setPersonalVariable).not.toHaveBeenCalled();
});

it("shows the reason the core refused a variable and keeps the draft", async () => {
  const user = userEvent.setup();
  keepVariables({});
  // The core's refusal names the broken rule as its whole message.
  services.personalVariable.setPersonalVariable = () => { throw new ConnectError("You can keep at most 100 Personal Variables.", Code.InvalidArgument); };
  renderSection();

  await user.click(await screen.findByRole("button", { name: "Add variable" }));
  const form = screen.getByRole("form", { name: "New variable" });
  await user.type(within(form).getByRole("textbox", { name: "Name" }), "EXTRA");
  await user.type(within(form).getByRole("textbox", { name: "Value" }), "value");
  await user.click(within(form).getByRole("button", { name: "Save" }));

  expect(await within(form).findByText("You can keep at most 100 Personal Variables.")).toBeInTheDocument();
  expect(within(form).getByRole("textbox", { name: "Name" })).toHaveValue("EXTRA");
  expect(within(form).getByRole("textbox", { name: "Value" })).toHaveValue("value");
});

it("reveals a value only on request and hides it again", async () => {
  const user = userEvent.setup();
  keepVariables({ DEPLOY_HOSTS: "alpha.example.test\nbeta.example.test", OPENAI_API_KEY: "sk-secret" });
  const reveal = recorder(({ name }: { name: string }) => ({ value: saved.get(name) }));
  services.personalVariable.revealPersonalVariable = reveal;
  renderSection();

  await user.click(await screen.findByRole("button", { name: "Show OPENAI_API_KEY" }));
  expect(await screen.findByText("sk-secret")).toBeInTheDocument();
  expect(reveal.calls).toMatchObject([{ name: "OPENAI_API_KEY" }]);

  await user.click(screen.getByRole("button", { name: "Show DEPLOY_HOSTS" }));
  expect(await screen.findByText("alpha.example.test")).toBeInTheDocument();
  expect(screen.getByText("+1 line")).toBeInTheDocument();
  expect(screen.queryByText("sk-secret")).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "Hide DEPLOY_HOSTS" }));
  expect(screen.queryByText("alpha.example.test")).not.toBeInTheDocument();
  expect(within(await row("DEPLOY_HOSTS")).getByText("Value hidden")).toBeInTheDocument();
});

it("never shows a value the person hid before it arrived", async () => {
  const user = userEvent.setup();
  keepVariables({ OPENAI_API_KEY: "sk-secret" });
  let answer!: () => void;
  services.personalVariable.revealPersonalVariable = () => new Promise(resolve => { answer = () => resolve({ value: "sk-secret" }); });
  renderSection();

  await user.click(await screen.findByRole("button", { name: "Show OPENAI_API_KEY" }));
  await user.click(screen.getByRole("button", { name: "Add variable" }));
  answer();

  expect(await screen.findByRole("form", { name: "New variable" })).toBeInTheDocument();
  expect(screen.queryByText("sk-secret")).not.toBeInTheDocument();
});

it("edits a multi-line value starting from the saved one", async () => {
  const user = userEvent.setup();
  keepVariables({ DEPLOY_HOSTS: "alpha.example.test" });
  renderSection();

  await user.click(await screen.findByRole("button", { name: "Edit DEPLOY_HOSTS" }));
  const form = await screen.findByRole("form", { name: "Edit DEPLOY_HOSTS" });
  expect(within(form).getByRole("textbox", { name: "Name" })).toHaveAttribute("readonly");
  const value = within(form).getByRole("textbox", { name: "Value" });
  expect(value).toHaveValue("alpha.example.test");
  await user.type(value, "{Enter}beta.example.test");
  await user.click(within(form).getByRole("button", { name: "Save" }));

  expect(await screen.findByRole("button", { name: "Edit DEPLOY_HOSTS" })).toHaveFocus();
  expect(services.personalVariable.setPersonalVariable).toHaveBeenCalledOnce();
  expect(saved.get("DEPLOY_HOSTS")).toBe("alpha.example.test\nbeta.example.test");
});

it("reports a value that could not be read and opens no editor", async () => {
  const user = userEvent.setup();
  keepVariables({ OPENAI_API_KEY: "sk-secret" });
  services.personalVariable.revealPersonalVariable = () => { throw new ConnectError("unavailable", Code.Unavailable); };
  renderSection();

  await user.click(await screen.findByRole("button", { name: "Edit OPENAI_API_KEY" }));

  expect(await screen.findByText("OPENAI_API_KEY couldn’t be read")).toBeInTheDocument();
  expect(screen.queryByRole("form")).not.toBeInTheDocument();
});

it("deletes a variable only once the person confirms", async () => {
  const user = userEvent.setup();
  keepVariables({ OPENAI_API_KEY: "sk-secret", DEPLOY_HOSTS: "a" });
  renderSection();

  await user.click(await screen.findByRole("button", { name: "Delete OPENAI_API_KEY" }));
  let dialog = await screen.findByRole("alertdialog", { name: "Delete OPENAI_API_KEY?" });
  expect(within(dialog).getByText("Works lose it the next time they start or resume.")).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus();
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
  expect(services.personalVariable.deletePersonalVariable).not.toHaveBeenCalled();

  await user.click(screen.getByRole("button", { name: "Delete OPENAI_API_KEY" }));
  dialog = await screen.findByRole("alertdialog", { name: "Delete OPENAI_API_KEY?" });
  await user.click(within(dialog).getByRole("button", { name: "Delete" }));

  await expect.poll(async () => (await rows()).length).toBe(1);
  expect(services.personalVariable.deletePersonalVariable).toHaveBeenCalledOnce();
  expect(saved.has("OPENAI_API_KEY")).toBe(false);
});

it("keeps a variable the core did not delete and says so", async () => {
  const user = userEvent.setup();
  keepVariables({ OPENAI_API_KEY: "sk-secret" });
  services.personalVariable.deletePersonalVariable = () => { throw new ConnectError("unavailable", Code.Unavailable); };
  renderSection();

  await user.click(await screen.findByRole("button", { name: "Delete OPENAI_API_KEY" }));
  await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Delete" }));

  expect(await screen.findByText("OPENAI_API_KEY couldn’t be deleted")).toBeInTheDocument();
  expect(await row("OPENAI_API_KEY")).toBeInTheDocument();
});

it("reports each failed deletion while another is still in flight", async () => {
  const user = userEvent.setup();
  keepVariables({ OPENAI_API_KEY: "sk-secret", DEPLOY_HOSTS: "a" });
  const failures = new Map<string, () => void>();
  services.personalVariable.deletePersonalVariable = ({ name }: { name: string }) =>
    new Promise((_, reject) => { failures.set(name, () => reject(new ConnectError("unavailable", Code.Unavailable))); });
  renderSection();

  await user.click(await screen.findByRole("button", { name: "Delete OPENAI_API_KEY" }));
  await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Delete" }));
  await user.click(screen.getByRole("button", { name: "Delete DEPLOY_HOSTS" }));
  await user.click(within(await screen.findByRole("alertdialog", { name: "Delete DEPLOY_HOSTS?" })).getByRole("button", { name: "Delete" }));
  await expect.poll(() => failures.size).toBe(2);

  failures.get("OPENAI_API_KEY")!();
  expect(await screen.findByText("OPENAI_API_KEY couldn’t be deleted")).toBeInTheDocument();
  await expect.poll(() => screen.getByRole("button", { name: "Delete OPENAI_API_KEY" }).hasAttribute("data-pending")).toBe(false);
  expect(screen.getByRole("button", { name: "Delete DEPLOY_HOSTS" })).toHaveAttribute("data-pending");

  failures.get("DEPLOY_HOSTS")!();
  expect(await screen.findByText("DEPLOY_HOSTS couldn’t be deleted")).toBeInTheDocument();
  expect(screen.getByText("OPENAI_API_KEY couldn’t be deleted")).toBeInTheDocument();
});

it("offers to load the variables again when loading failed", async () => {
  const user = userEvent.setup();
  keepVariables({ OPENAI_API_KEY: "sk-secret" });
  const list = services.personalVariable.listPersonalVariables!;
  services.personalVariable.listPersonalVariables = () => { throw new ConnectError("unavailable", Code.Unavailable); };
  renderSection();

  expect(await screen.findByText("Variables couldn’t be loaded")).toBeInTheDocument();
  services.personalVariable.listPersonalVariables = list;
  await user.click(screen.getByRole("button", { name: "Try again" }));

  expect(await screen.findByRole("button", { name: "Show OPENAI_API_KEY" })).toBeInTheDocument();
});
