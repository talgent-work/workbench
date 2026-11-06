import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FilePreview } from "../components/preview/file-preview";
import { decodeUtf8Prefix, type PreviewVerdict } from "../components/preview/preview-verdict";

type Shown = Omit<PreviewVerdict, "rendering"> & { rendering?: boolean };

function show(name: string, shown: Shown, onDownload = vi.fn()) {
  const verdict: PreviewVerdict = { rendering: false, ...shown };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const loadVerdict = vi.fn(async () => verdict);
  const view = render(<QueryClientProvider client={client}>
    <FilePreview name={name} verdictKey={[name]} loadVerdict={loadVerdict} toolbar={null} onDownload={onDownload} />
  </QueryClientProvider>);
  return { ...view, loadVerdict, onDownload };
}

afterEach(() => vi.restoreAllMocks());

describe("FilePreview follows Storage's verdict", () => {
  it("offers the original for a refused file without reading its content", async () => {
    const user = userEvent.setup();
    const fetchContent = vi.spyOn(globalThis, "fetch");
    const { onDownload } = show("deck.pptx", { mode: "refused", display: "none", refusal: "too_large", limitBytes: 20 << 20 });
    await user.click(await screen.findByRole("button", { name: "Download original" }));
    expect(onDownload).toHaveBeenCalled();
    expect(fetchContent).not.toHaveBeenCalled();
  });

  it("reads only the shown start of a truncated file, cut at a whole character", async () => {
    const bytes = new TextEncoder().encode("ab中"); // 中 is three bytes; four are shown
    const fetchContent = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(bytes.subarray(0, 4), { status: 206 }));
    const { container } = show("notes.txt", { mode: "truncated", display: "text", limitBytes: 4, url: "/content/notes" });
    await waitFor(() => expect(container.querySelector("pre")).toHaveTextContent(/^1?ab$/));
    expect(fetchContent).toHaveBeenCalledWith("/content/notes", expect.objectContaining({ headers: { Range: "bytes=0-3" } }));
  });

  it("shows the truncated source of markup too large to render", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("<html><script>x()</script>", { status: 206 }));
    const { container } = show("page.html", { mode: "truncated", display: "text", limitBytes: 1 << 20, url: "/content/page" });
    await waitFor(() => expect(container.querySelector("pre")).toHaveTextContent("<html><script>x()</script>"));
    expect(container.querySelector("iframe")).toBeNull();
  });
});

describe("decodeUtf8Prefix", () => {
  it("drops only an incomplete trailing character", () => {
    const bytes = new TextEncoder().encode("中文");
    expect(decodeUtf8Prefix(bytes)).toBe("中文");
    expect(decodeUtf8Prefix(bytes.subarray(0, 5))).toBe("中");
    expect(decodeUtf8Prefix(bytes.subarray(0, 4))).toBe("中");
    expect(decodeUtf8Prefix(bytes.subarray(0, 3))).toBe("中");
  });
});

describe("FilePreview renders a Preview Copy on opening", () => {
  const showRendering = (name: string, loadVerdict: () => Promise<PreviewVerdict>, requestRender: () => Promise<PreviewVerdict>) => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(<QueryClientProvider client={client}>
      <FilePreview name={name} verdictKey={[name]} loadVerdict={loadVerdict} requestRender={requestRender} toolbar={null} onDownload={vi.fn()} />
    </QueryClientProvider>);
  };
  const needsRendering: PreviewVerdict = { mode: "render", display: "pdf", limitBytes: 0, rendering: false };
  const rendering: PreviewVerdict = { ...needsRendering, rendering: true };
  const copy: PreviewVerdict = { mode: "preview_copy", display: "pdf", limitBytes: 0, rendering: false, url: "/copy" };

  it("asks once and switches to the copy when it is ready", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("%PDF-1.7"));
      vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:copy");
      vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
      const loadVerdict = vi.fn().mockResolvedValueOnce(needsRendering).mockResolvedValueOnce(rendering).mockResolvedValue(copy);
      const requestRender = vi.fn().mockResolvedValue(rendering);
      showRendering("deck.pptx", loadVerdict, requestRender);
      expect(await screen.findByRole("button", { name: "Download original" })).toBeInTheDocument();
      await vi.advanceTimersByTimeAsync(4500);
      await waitFor(() => expect(screen.getByTitle("deck.pptx")).toHaveAttribute("src", "blob:copy"));
      expect(requestRender).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("offers Retry when the rendering ended without a copy, without asking again on its own", async () => {
    const user = userEvent.setup();
    const loadVerdict = vi.fn().mockResolvedValueOnce(needsRendering).mockResolvedValue(needsRendering);
    const requestRender = vi.fn().mockResolvedValueOnce(needsRendering).mockResolvedValueOnce(rendering);
    showRendering("deck.pptx", loadVerdict, requestRender);
    const retry = await screen.findByRole("button", { name: "Retry" });
    expect(requestRender).toHaveBeenCalledTimes(1);
    await user.click(retry);
    expect(requestRender).toHaveBeenCalledTimes(2);
  });

  it("stops polling once closed", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const loadVerdict = vi.fn().mockResolvedValue(rendering);
      const view = showRendering("deck.pptx", loadVerdict, vi.fn());
      await vi.waitFor(() => expect(loadVerdict).toHaveBeenCalled());
      await vi.advanceTimersByTimeAsync(4500);
      const calls = loadVerdict.mock.calls.length;
      expect(calls).toBeGreaterThanOrEqual(2);
      view.unmount();
      await vi.advanceTimersByTimeAsync(6000);
      expect(loadVerdict).toHaveBeenCalledTimes(calls);
    } finally {
      vi.useRealTimers();
    }
  });

  it("never lets a late answer replace the file now shown", async () => {
    let answer: (verdict: PreviewVerdict) => void = () => {};
    const requestRender = vi.fn(() => new Promise<PreviewVerdict>(resolve => { answer = resolve; }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(<QueryClientProvider client={client}>
      <FilePreview key="a" name="a.pptx" verdictKey={["a"]} loadVerdict={async () => needsRendering} requestRender={requestRender} toolbar={null} onDownload={vi.fn()} />
    </QueryClientProvider>);
    await waitFor(() => expect(requestRender).toHaveBeenCalled());
    view.rerender(<QueryClientProvider client={client}>
      <FilePreview key="b" name="b.md" verdictKey={["b"]} loadVerdict={async () => ({ mode: "refused", display: "none", refusal: "encrypted", limitBytes: 0, rendering: false })} toolbar={null} onDownload={vi.fn()} />
    </QueryClientProvider>);
    expect(await screen.findByText("This file is encrypted.")).toBeInTheDocument();
    answer(copy);
    await new Promise(resolve => setTimeout(resolve, 50));
    expect(screen.getByText("This file is encrypted.")).toBeInTheDocument();
    expect(client.getQueryData(["file-preview-verdict", "a"])).toBeUndefined();
  });
});
