import { useId, useState, type ReactNode } from "react";
import { Button } from "@heroui/react";
import { ChevronRight } from "lucide-react";
import "./rail.css";

// One collapsible section of a context rail: a heading that toggles it, an
// optional summary that stays readable while it is collapsed, and an optional
// action beside the heading.
export function RailSection({ title, summary, children, action, defaultOpen = true, open: controlledOpen, onOpenChange, className = "" }: { title: string; summary?: string; children: ReactNode; action?: ReactNode; defaultOpen?: boolean; open?: boolean; onOpenChange?: (open: boolean) => void; className?: string }) {
  const [localOpen, setLocalOpen] = useState(defaultOpen);
  const open = controlledOpen ?? localOpen;
  const setOpen = onOpenChange ?? setLocalOpen;
  const contentId = useId();
  return <section className={`cr-section ${className}`} aria-label={title}>
    <div className="cr-heading"><h3><Button variant="ghost" className="cr-section-trigger" aria-label={title} aria-describedby={summary ? `${contentId}-summary` : undefined} aria-expanded={open} aria-controls={contentId} onPress={() => setOpen(!open)}>
      <span>{title}</span>{summary && <span id={`${contentId}-summary`} className="cr-summary">{summary}</span>}<ChevronRight size={14} className="cr-expand" aria-hidden="true" />
    </Button></h3>{action}</div>
    <div id={contentId} hidden={!open}>{children}</div>
  </section>;
}
