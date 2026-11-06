import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createElement } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { LoginPage } from "../pages/login-page";

let locationAssignMock: ReturnType<typeof vi.fn>;

function renderLogin(path: string) {
  return render(createElement(MemoryRouter, { initialEntries: [path] }, createElement(LoginPage)));
}

describe("sign-in page", () => {
  beforeEach(() => {
    locationAssignMock = vi.fn();
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { assign: locationAssignMock, href: "http://localhost/login", origin: "http://localhost", pathname: "/login", search: "" },
    });
  });

  it("starts sign-in and returns to the requested page on this origin", async () => {
    renderLogin("/login?next=/projects/project-api/works?filter=open");
    await userEvent.setup().click(screen.getByRole("button", { name: "Continue with GitHub" }));
    expect(locationAssignMock).toHaveBeenCalledWith(`/auth/github/login?next=${encodeURIComponent(`${window.location.origin}/projects/project-api/works?filter=open`)}`);
  });

  it("explains a known sign-in failure and offers to try again", () => {
    renderLogin("/login?error=denied");
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue with GitHub" })).toBeInTheDocument();
  });

  it("shows no notice for an unknown error code", () => {
    renderLogin("/login?error=whatever");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
