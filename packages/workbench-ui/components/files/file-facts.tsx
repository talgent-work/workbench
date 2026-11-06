import type { ReactNode } from "react";

/**
 * A file's facts on hover or focus, one per line: the card the context rail's
 * files and the workspace tree share.
 */
export function FileFacts({ name, sizeBytes, modifiedAt, children }: { name: string; sizeBytes?: number; modifiedAt?: Date | null; children?: ReactNode }) {
  const extension = /\.([^.]+)$/.exec(name)?.[1];
  return <>
    <strong>{name}</strong>
    {extension && <span>{extension.toUpperCase()}</span>}
    {sizeBytes !== undefined && <span>{new Intl.NumberFormat("en").format(sizeBytes)} bytes</span>}
    {modifiedAt && <span>{modifiedAt.toLocaleString("en", { dateStyle: "medium", timeStyle: "short" })}</span>}
    {children}
  </>;
}
