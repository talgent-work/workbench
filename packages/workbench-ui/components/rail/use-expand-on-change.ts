import { useState } from "react";

// Whether a rail section is expanded, opening it when its content changes
// while the rail is in view. `content` names what the section shows, undefined
// until it is known; the first known content is where changes count from, and
// a new `scope` (another subject) starts over at the section's default.
export function useExpandOnChange({ content, scope, visible, defaultOpen }: { content: string | undefined; scope: string; visible: boolean; defaultOpen: boolean }) {
  const [open, setOpen] = useState(defaultOpen);
  const [seen, setSeen] = useState<{ scope: string; content: string | undefined }>({ scope, content });
  if (seen.scope !== scope) {
    setSeen({ scope, content });
    setOpen(defaultOpen);
  } else if (content !== undefined && content !== seen.content) {
    setSeen({ scope, content });
    if (seen.content !== undefined && visible) setOpen(true);
  }
  return [open, setOpen] as const;
}
