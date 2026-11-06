import { Button, Tooltip } from "@heroui/react";
import { List } from "lucide-react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { useReadingArea } from "../shell/reading-context";
import "./context-rail.css";

/** The Work context card every Work page shows beside its conversation: the
 * card collapses from the top bar's toggle, and closing a reading in the
 * reading area makes room for it. The toggle and card exist only while some
 * section has something to show; `beside` holds what stays outside the card.
 * A Project's Home shows the Project in the same card under its own `label`. */
export function WorkContextRail({ label = "Work context", hasContent, collapsed, onCollapsedChange, beside, children }: { label?: string; hasContent: boolean; collapsed: boolean; onCollapsedChange: (collapsed: boolean) => void; beside?: ReactNode; children: ReactNode }) {
  const reader = useReadingArea();
  const shown = !reader.content && !collapsed;
  return <aside className="cr-rail" data-collapsed={!shown} aria-label={label}>
    {/* The context toggle has a slot of its own so it stays rightmost whichever
        topbar buttons appear first. */}
    {hasContent && reader.contextToggleToolbar && createPortal(<Tooltip><Button aria-label={label} aria-pressed={shown} variant={shown ? "tertiary" : "ghost"} size="sm" isIconOnly onPress={() => { if (reader.content) reader.close(); onCollapsedChange(shown); }}><List size={17} aria-hidden="true" /></Button><Tooltip.Content>{label}</Tooltip.Content></Tooltip>, reader.contextToggleToolbar)}
    {beside}
    {hasContent && <div className="cr-card" hidden={!shown}>{children}</div>}
  </aside>;
}
