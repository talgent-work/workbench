import { Button, Tooltip } from "@heroui/react";
import { Download } from "lucide-react";
import { createPortal } from "react-dom";
import type { PreviewVerdict } from "../preview/preview-verdict";
import { FilePreview } from "../preview/file-preview";

/** One file in the reading area: its preview, and its download in the
 * reading area's top bar. */
export function FileReading({ name, verdictKey, loadVerdict, requestRender, toolbar, download }: {
  name: string;
  verdictKey: readonly unknown[];
  loadVerdict: (signal: AbortSignal) => Promise<PreviewVerdict>;
  requestRender: () => Promise<PreviewVerdict>;
  toolbar: HTMLElement | null;
  download: { run: () => void; isPending: boolean; error: Error | null; reset: () => void };
}) {
  return <>
    {toolbar && createPortal(<Tooltip><Button isIconOnly size="sm" variant="ghost" aria-label="Download file" isPending={download.isPending} onPress={download.run}><Download size={16} /></Button><Tooltip.Content>Download</Tooltip.Content></Tooltip>, toolbar)}
    {download.error && <p role="alert">{download.error.message} <Button variant="ghost" size="sm" onPress={download.reset}>Dismiss</Button></p>}
    <FilePreview name={name} toolbar={toolbar} onDownload={download.run} verdictKey={verdictKey} loadVerdict={loadVerdict} requestRender={requestRender} />
  </>;
}
