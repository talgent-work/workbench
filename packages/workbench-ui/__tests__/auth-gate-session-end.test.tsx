import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { AuthGate } from "../components/auth/auth-gate";
import { refreshSession } from "../lib/session";

vi.mock("../lib/account-service", () => ({
  useCurrentUser: () => ({ isLoading: false, isError: false, data: { user: { id: "01a11fb2-e6c2-701f-80b7-38c54aa2eae9" } } }),
}));

function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location">{`${location.pathname}${location.search}`}</div>;
}

afterEach(() => vi.unstubAllGlobals());

// Intent: a revoked or expired Login Session ends on this device at once; the
// page neither waits for its next session check nor keeps asking Account to
// refresh a session that is gone.
it("sends a signed-in person to sign in as soon as Account rejects the session's refresh", async () => {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={["/works/w-1"]}>
        <Routes>
          <Route path="/works/:id" element={<AuthGate><p>Work page</p></AuthGate>} />
          <Route path="/login" element={<p>Sign in</p>} />
        </Routes>
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(screen.getByText("Work page")).toBeInTheDocument();
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 401 }));
  vi.stubGlobal("fetch", fetchMock);

  expect(await refreshSession()).toBe("rejected");

  await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("/login?next=%2Fworks%2Fw-1"));
  expect(await refreshSession()).toBe("rejected");
  expect(fetchMock).toHaveBeenCalledTimes(1);
});
