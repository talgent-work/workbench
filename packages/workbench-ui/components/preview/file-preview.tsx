import { Alert, Button, Spinner } from "@heroui/react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { fileExtension, textFormat } from "./file-format";
import { decodeUtf8Prefix, formatBytes, refusalText, type PreviewVerdict } from "./preview-verdict";
import { SourcePreview, TextPreview } from "./text-preview";

type Shown = { text: string; blob: null } | { text: null; blob: Blob };

// While a Preview Copy is made the verdict is asked again this often.
const renderingPollMs = 2000;

// Workbooks render as printed pages, which may leave cells out.
const workbookExtensions = new Set(["xlsx", "xls", "ods"]);

/**
 * One file shown as Storage's verdict says: directly, truncated, as its
 * Preview Copy, or refused with its reason and the original to download.
 * A file needing a Preview Copy asks for it on opening, then polls until it
 * shows; closing or switching files stops polling, and a late answer never
 * replaces the current file's. `verdictKey` identifies the file's current
 * content; a new key asks again. `requestRender` is absent where no Preview
 * Copy can be made.
 */
export function FilePreview({ name, verdictKey, loadVerdict, requestRender, toolbar, onDownload }: {
  name: string;
  verdictKey: readonly unknown[];
  loadVerdict: (signal: AbortSignal) => Promise<PreviewVerdict>;
  requestRender?: () => Promise<PreviewVerdict>;
  toolbar: HTMLElement | null;
  onDownload: () => void;
}) {
  const queryClient = useQueryClient();
  const queryKey = ["file-preview-verdict", ...verdictKey];
  const verdict = useQuery({
    queryKey,
    queryFn: ({ signal }) => loadVerdict(signal),
    retry: false, gcTime: 0, refetchOnWindowFocus: false,
    refetchInterval: query => query.state.data?.mode === "render" && query.state.data.rendering ? renderingPollMs : false,
  });
  const render = useMutation({
    mutationFn: async () => {
      if (!requestRender) throw new Error("No Preview Copy can be made here.");
      return requestRender();
    },
  });
  const ask = () => render.mutate(undefined, {
    // Per-call callbacks never run after unmount, so a late answer is dropped.
    onSuccess: answer => queryClient.setQueryData(queryKey, answer),
  });
  const firstAsk = verdict.data?.mode === "render" && !verdict.data.rendering && Boolean(requestRender) && render.isIdle;
  useEffect(() => {
    // The first opening asks once; after that only Retry asks again.
    if (firstAsk) ask();
    // ask is rebuilt each render; firstAsk alone decides when to call it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [firstAsk]);
  const readable = verdict.data && (verdict.data.mode === "direct" || verdict.data.mode === "truncated" || verdict.data.mode === "preview_copy") && verdict.data.url;
  const shown = useQuery({
    // The link reads once and expires, so its URL identifies this read.
    queryKey: ["file-preview-content", verdict.data?.url],
    enabled: Boolean(readable),
    queryFn: ({ signal }) => readShown(verdict.data!, signal),
    retry: false, gcTime: 0, staleTime: Infinity, refetchOnWindowFocus: false,
  });
  // A failed read is retried with a new link from a new verdict.
  const retry = () => void verdict.refetch();

  if (verdict.isPending) return <p className="sr-only" role="status">Loading preview…</p>;
  if (verdict.isError) return <PreviewAlert status="danger" message="Could not load the preview." action={<Button size="sm" variant="secondary" onPress={retry}>Retry preview</Button>} />;
  const { data } = verdict;
  if (data.mode === "refused") {
    return <PreviewAlert status="default" message={refusalText(data)} action={<DownloadOriginal onDownload={onDownload} />} />;
  }
  if (data.mode === "render") {
    if (!requestRender) return <PreviewAlert status="default" message="Preview isn't available for this kind of file." action={<DownloadOriginal onDownload={onDownload} />} />;
    if (data.rendering || render.isPending || render.isIdle) {
      return <PreviewAlert status="default" indicator={<Spinner size="sm" />} message="Generating preview… It appears here when ready." action={<DownloadOriginal onDownload={onDownload} />} />;
    }
    // The rendering ended without a copy: a temporary failure, or the request failed.
    return <PreviewAlert status="default" message="Preview isn't available right now."
      action={<div className="pv-actions"><Button size="sm" variant="secondary" onPress={ask}>Retry</Button><DownloadOriginal onDownload={onDownload} /></div>} />;
  }
  return <div className="pr-file-document">
    {shown.isPending && <p className="sr-only" role="status">Loading preview…</p>}
    {shown.isError && <PreviewAlert status="danger" message="Could not load this file." action={<Button size="sm" variant="secondary" onPress={retry}>Retry preview</Button>} />}
    {data.mode === "truncated" && <TruncationNotice name={name} verdict={data} />}
    {data.mode === "preview_copy" && workbookExtensions.has(fileExtension(name) ?? "") && <PreviewAlert status="default" message="Previewed as printed pages; some cells may not appear. Download the original for the full workbook." />}
    {shown.data && (shown.data.text !== null
      ? <ShownText name={name} text={shown.data.text} truncated={data.mode === "truncated"} toolbar={toolbar} />
      : <BinaryPreview key={shown.dataUpdatedAt} blob={shown.data.blob} name={name} image={data.display === "image"} onRetry={retry} />)}
  </div>;
}

async function readShown(verdict: PreviewVerdict, signal: AbortSignal): Promise<Shown> {
  const truncated = verdict.mode === "truncated";
  const response = await fetch(verdict.url!, { signal, headers: truncated ? { Range: `bytes=0-${verdict.limitBytes - 1}` } : undefined });
  if (!response.ok) throw new Error("Could not load this file.");
  if (verdict.display === "text") {
    const bytes = new Uint8Array(await response.arrayBuffer());
    // A server ignoring Range still sends the whole file; show only the start.
    return { text: decodeUtf8Prefix(truncated ? bytes.subarray(0, verdict.limitBytes) : bytes), blob: null };
  }
  // Fetch the authorized bytes before rendering: the content endpoint denies
  // framing, and a link does not prove the read will succeed.
  return { text: null, blob: new Blob([await response.blob()], { type: verdict.display === "pdf" ? "application/pdf" : response.headers.get("Content-Type") ?? "" }) };
}

function isMarkup(name: string) {
  const kind = textFormat(name).kind;
  return kind === "html" || kind === "svg";
}

// Truncated markup is never rendered: its source shows instead.
function ShownText({ name, text, truncated, toolbar }: { name: string; text: string; truncated: boolean; toolbar: HTMLElement | null }) {
  return truncated && isMarkup(name) ? <SourcePreview path={name} content={text} /> : <TextPreview path={name} content={text} toolbar={toolbar} />;
}

function TruncationNotice({ name, verdict }: { name: string; verdict: PreviewVerdict }) {
  const limit = formatBytes(verdict.limitBytes);
  const message = isMarkup(name)
    ? `This file is over ${limit}, too large to render. Showing the source of its first ${limit}.`
    : `Showing the first ${limit} of this file.`;
  return <PreviewAlert status="default" message={message} />;
}

function PreviewAlert({ status, message, action, indicator }: { status: "default" | "danger"; message: string; action?: ReactNode; indicator?: ReactNode }) {
  return <Alert status={status} className="pv-alert" role={status === "danger" ? "alert" : "status"}>
    <Alert.Indicator>{indicator}</Alert.Indicator>
    <Alert.Content><Alert.Description>{message}</Alert.Description></Alert.Content>
    {action}
  </Alert>;
}

function DownloadOriginal({ onDownload }: { onDownload: () => void }) {
  return <Button size="sm" variant="secondary" onPress={onDownload}><Download size={14} />Download original</Button>;
}

function BinaryPreview({ blob, name, image, onRetry }: { blob: Blob; name: string; image: boolean; onRetry: () => void }) {
  const imageElement = useRef<HTMLImageElement>(null);
  const frameElement = useRef<HTMLIFrameElement>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const element = imageElement.current ?? frameElement.current;
    if (!element) return;
    const url = URL.createObjectURL(blob);
    element.src = url;
    return () => URL.revokeObjectURL(url);
  }, [blob]);
  if (failed) return <PreviewAlert status="danger" message="Could not display this file." action={<Button size="sm" variant="secondary" onPress={onRetry}>Retry preview</Button>} />;
  return image ? <img ref={imageElement} alt={name} onError={() => setFailed(true)} /> : <iframe ref={frameElement} title={name} onError={() => setFailed(true)} />;
}
