import { Check, Circle } from "lucide-react";
import type { WorkProgress } from "../../lib/work-progress";
import { RailSection } from "../rail/rail-section";
import { useExpandOnChange } from "../rail/use-expand-on-change";

// Progress belongs to the Work, independently of the business page showing it.
// Undefined is not loaded; null is a successful read with no items.
// The owning rail calls this even while empty, so the first later Progress
// report counts as a change and expands just like any subsequent report.
export function useWorkProgressSection({ progress, scope, visible }: { progress: WorkProgress | null | undefined; scope: string; visible: boolean }) {
  const items = progress?.items ?? [];
  const [open, setOpen] = useExpandOnChange({ scope, visible, defaultOpen: false,
    content: progress === undefined ? undefined : items.map(item => `${item.status} ${item.content}`).join("\n") });
  if (!items.length) return null;
  return <RailSection title="Progress" summary={`${items.filter(item => item.status === "completed").length} of ${items.length} done`} open={open} onOpenChange={setOpen}>
    <ul className="cr-todos" aria-label="Work progress">{items.map((item, index) => <li key={index} data-status={item.status} title={item.content}>
      {item.status === "completed" ? <Check size={13} aria-label="Completed" /> : item.status === "in_progress" ? <span className="cr-todo-now" role="img" aria-label="In progress" /> : <Circle size={12} aria-label="Pending" />}<span>{item.content}</span>
    </li>)}</ul>
  </RailSection>;
}
