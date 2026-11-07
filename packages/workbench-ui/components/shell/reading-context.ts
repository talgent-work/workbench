import { createContext, useContext, type ReactNode } from "react";
import type { WorkFile } from "@talgent/contracts/agent/public/v1/work_file_pb";
import type { ToolPreview } from "../work/work-message-flow";

/** What the reading area can show, by kind. `label` names what the reading
 * belongs to, the first crumb of its location. An extension adds its own
 * kinds by augmenting this interface and renders them with its own
 * ReadingRenderer. */
export interface ReadingKinds {
  tool: { label: string; tool: ToolPreview };
  /** The Work's Token Usage, its state read from the Work itself. */
  trace: { label: string; projectId: string; workId: string; createdAt: Date | null };
  "work-file": { label: string; workId: string; file: WorkFile };
}

export type ReadingContent = { [Kind in keyof ReadingKinds]: { kind: Kind } & ReadingKinds[Kind] }[keyof ReadingKinds];

export type ReadingSlots = { toolbar: HTMLDivElement | null; location: HTMLDivElement | null };

/** How one reading shows: `ownLocation` gives it the location slot to name its
 * own path; otherwise its location is its label and title. */
export type ReadingView = { key: string; label: string; title: string; ownLocation?: boolean; render: (slots: ReadingSlots) => ReactNode };

export type ReadingRenderer = (content: ReadingContent) => ReadingView | null;

export const ReadingContext = createContext<{
  content: ReadingContent | null;
  open: (content: ReadingContent, trigger?: HTMLElement) => void;
  close: () => void;
  contextToolbar: HTMLDivElement | null;
  // The Work context card's toggle, always the topbar's last button.
  contextToggleToolbar: HTMLDivElement | null;
  workStatusToolbar: HTMLDivElement | null;
  toolbar: HTMLDivElement | null;
} | null>(null);

export function useReadingArea() {
  const value = useContext(ReadingContext);
  if (!value) throw new Error("Reading area requires AppShell");
  return value;
}
