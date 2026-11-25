import { Code, ConnectError } from "@connectrpc/connect";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AuthGate } from "../components/auth/auth-gate";
import { ToastRegion } from "../components/feedback/toast-region";
import { currentUserQueryKey } from "../lib/account-service";
import { recorder, resetServices, services, userId } from "../test-support/backend";

vi.mock("../lib/transport", async () => (await import("../test-support/backend")).transportModule);

const signedIn = () => ({ session: { userId, displayName: "Octo Cat", username: "octocat", primaryEmail: "octo@example.test" } });
const unavailable = () => { throw new ConnectError("session unavailable", Code.Unavailable); };
const recheckNotice = "Couldn’t check your sign-in";

function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location">{`${location.pathname}${location.search}`}</div>;
}

function renderGate() {
  const client = new QueryClient();
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/works/w-1"]}>
        <Routes>
          <Route path="/works/:id" element={<AuthGate><p>Work page</p></AuthGate>} />
          <Route path="/login" element={<p>Sign in</p>} />
        </Routes>
        <LocationProbe />
      </MemoryRouter>
      <ToastRegion />
    </QueryClientProvider>,
  );
  return client;
}

const settle = () => act(() => vi.advanceTimersByTimeAsync(0));
const pass = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));
const recheck = (client: QueryClient) => act(() => { void client.refetchQueries({ queryKey: currentUserQueryKey }); });

beforeEach(() => {
  vi.useFakeTimers();
  resetServices();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

// Intent: a session check that fails after the page loaded, as on returning
// to a tab after the device slept, must not replace the page the person was
// using; they learn the check failed and can run it again.
it("keeps a loaded page when re-checking the session fails, and offers to check again", async () => {
  const client = renderGate();
  await settle();
  expect(screen.getByText("Work page")).toBeInTheDocument();

  const getCurrentSession = recorder(unavailable);
  services.session.getCurrentSession = getCurrentSession;
  await recheck(client);
  await pass(1_000);

  expect(getCurrentSession).toHaveBeenCalledTimes(2);
  expect(screen.getByText("Work page")).toBeInTheDocument();
  expect(screen.queryByText("Unable to load profile")).not.toBeInTheDocument();
  expect(screen.getByText(recheckNotice)).toBeInTheDocument();
  expect(screen.getByTestId("location")).toHaveTextContent("/works/w-1");

  services.session.getCurrentSession = signedIn;
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await pass(1_000);

  expect(screen.getByText("Work page")).toBeInTheDocument();
  expect(screen.queryByText(recheckNotice)).not.toBeInTheDocument();
});

// Intent: the first call after the device wakes often fails before the
// network is back; one bounded retry absorbs it without any notice.
it("absorbs one failed session check without a notice", async () => {
  const client = renderGate();
  await settle();

  let calls = 0;
  services.session.getCurrentSession = () => (++calls === 1 ? unavailable() : signedIn());
  await recheck(client);
  await pass(1_000);

  expect(calls).toBe(2);
  expect(screen.getByText("Work page")).toBeInTheDocument();
  expect(screen.queryByText(recheckNotice)).not.toBeInTheDocument();
});

// Intent: with no profile loaded yet there is no page to keep, so a session
// check that keeps failing shows the in-app error instead of sign-in.
it("shows the profile error when the first session check keeps failing", async () => {
  const getCurrentSession = recorder(unavailable);
  services.session.getCurrentSession = getCurrentSession;
  renderGate();
  await pass(1_000);

  expect(getCurrentSession).toHaveBeenCalledTimes(2);
  expect(screen.getByRole("alert")).toHaveTextContent("Unable to load profile");
  expect(screen.queryByText("Work page")).not.toBeInTheDocument();
});

// Intent: a session found gone is final; the person signs in again at once
// and Account is not asked again.
it("sends a signed-in person to sign in when a re-check finds the session gone", async () => {
  const client = renderGate();
  await settle();

  const getCurrentSession = recorder(() => { throw new ConnectError("unauthenticated", Code.Unauthenticated); });
  services.session.getCurrentSession = getCurrentSession;
  await recheck(client);
  await pass(1_000);

  expect(getCurrentSession).toHaveBeenCalledTimes(1);
  expect(screen.getByTestId("location")).toHaveTextContent("/login?next=%2Fworks%2Fw-1");
  expect(screen.queryByText(recheckNotice)).not.toBeInTheDocument();
});
