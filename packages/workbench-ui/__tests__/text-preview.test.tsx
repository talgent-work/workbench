import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { textFormat } from "../components/preview/file-format";
import { TextPreview } from "../components/preview/text-preview";

describe("format routing by extension", () => {
  // One row per routing branch: extension, case, multi-dot and bare file names, unknown.
  it.each([
    ["README.markdown", "markdown"], ["page.HTM", "html"], ["logo.svg", "svg"], ["fix.patch", "diff"],
    ["build.gradle.kts", "code"], ["Dockerfile", "code"], ["data.csv", "text"], ["LICENSE", "text"], ["archive.unknownext", "text"],
  ])("%s → %s", (path, kind) => {
    expect(textFormat(path).kind).toBe(kind);
  });
});

describe("TextPreview", () => {
  it("shows a .log holding HTML as plain text, never as a page", async () => {
    const { container } = render(<TextPreview path="server.log" content={'<div onclick="x()">hi</div><script>alert(1)</script>'} />);
    expect(container.querySelector("iframe, script, div[onclick]")).toBeNull();
    await waitFor(() => expect(container.querySelector("pre")).toHaveTextContent('<div onclick="x()">hi</div>'));
  });

  it("isolates HTML files in a sandboxed frame", () => {
    render(<TextPreview path="index.html" content="<h1>Report</h1>" />);
    const frame = screen.getByTitle("HTML preview");
    expect(frame).toHaveAttribute("sandbox", "allow-scripts");
    expect(frame.getAttribute("srcdoc")).toContain("Content-Security-Policy");
  });

  it("shows an SVG as an image, so its scripts never run", () => {
    const svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 5"><script>alert(1)</script></svg>';
    const { container } = render(<TextPreview path="logo.svg" content={svg} />);
    expect(screen.getByRole("img", { name: "logo.svg" }).getAttribute("src")).toMatch(/^data:image\/svg\+xml/);
    expect(container.querySelector("iframe, script")).toBeNull();
  });
});
