import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { CornerDownLeft, Sparkles } from "lucide-react";
import "./skill-completion.css";

// A command or Skill offered after "/". Start Work offers the Skills declared by
// the selected Plugins, grouped by Plugin; a running Work offers what its
// Harness reported, which carries no reliable category.
export interface SlashCandidate { name: string; description: string; argumentHint: string; group?: string; builtIn?: boolean }

const tokenOf = (value: string) => /(?:^|\s)\/([\w:.-]*)$/.exec(value)?.[1];
const recentLimit = 3;

/**
 * Completes the "/" token at the end of the draft. Every match is listed and
 * scrolls; the composer's recently used entries come first while nothing is
 * typed after the slash. `grouped` sections the rest by `group`.
 */
export function useSlashCompletion(value: string, setValue: (value: string) => void, candidates: SlashCandidate[], listId: string, grouped = false) {
  const [active, setActive] = useState(0);
  const [dismissed, setDismissed] = useState("");
  const [recent, setRecent] = useState<string[]>([]);
  const list = useRef<HTMLDivElement>(null);
  const token = tokenOf(value);
  const open = token !== undefined && dismissed !== value;
  const query = token?.toLowerCase() ?? "";
  const filtered = open ? candidates.filter(item => `${item.name} ${item.description}`.toLowerCase().includes(query)) : [];
  const recentItems = query ? [] : recent.map(name => filtered.find(item => item.name === name)).filter((item): item is SlashCandidate => Boolean(item));
  const rest = filtered.filter(item => !recentItems.includes(item));
  const sections = [
    ...(recentItems.length ? [{ title: "Recent", items: recentItems }] : []),
    ...(grouped ? [...new Set(rest.map(item => item.group ?? ""))].map(title => ({ title, items: rest.filter(item => (item.group ?? "") === title) }))
      : [{ title: recentItems.length ? "All" : "", items: rest }]),
  ].filter(section => section.items.length);
  const matches = sections.flatMap(section => section.items);
  const current = Math.min(active, Math.max(matches.length - 1, 0));
  // Keeps the active option in view; environments without layout (jsdom) lack scrollIntoView.
  useEffect(() => { if (open) list.current?.querySelector(`[data-index="${current}"]`)?.scrollIntoView?.({ block: "nearest" }); }, [open, current]);
  const pick = (item: SlashCandidate) => {
    setValue(value.replace(/\/[\w:.-]*$/, `/${item.name} `)); setActive(0);
    setRecent(names => [item.name, ...names.filter(name => name !== item.name)].slice(0, recentLimit));
  };
  const onKeyDown = (event: KeyboardEvent) => {
    if (!open || !matches.length) return false;
    if (event.key === "ArrowDown") { event.preventDefault(); setActive((current + 1) % matches.length); return true; }
    if (event.key === "ArrowUp") { event.preventDefault(); setActive((current - 1 + matches.length) % matches.length); return true; }
    if (event.key === "Enter" || event.key === "Tab") { event.preventDefault(); pick(matches[current]); return true; }
    if (event.key === "Escape") { event.preventDefault(); setDismissed(value); return true; }
    return false;
  };
  let index = 0;
  const row = (item: SlashCandidate) => {
    const position = index++;
    return <div key={item.name} id={`${listId}-${position}`} data-index={position} role="option" aria-selected={position === current} className="sk-slash-row"
      onMouseDown={event => { event.preventDefault(); pick(item); }} onMouseEnter={() => setActive(position)}>
      <Sparkles size={15} className="sk-muted" aria-hidden="true" />
      <span className="sk-slash-name">/{item.name}{item.argumentHint && <span className="sk-muted"> {item.argumentHint}</span>}</span>
      <span className="sk-muted sk-slash-desc">{item.description}</span>
      {position === current && <CornerDownLeft size={13} className="sk-muted" aria-hidden="true" />}
    </div>;
  };
  const menu = open ? <div ref={list} className="sk-slash" role="listbox" id={listId} aria-label="Commands and Skills">
    {matches.length ? sections.map(section => section.title
      ? <div key={section.title} role="group" aria-label={section.title}><div className="sk-slash-heading" aria-hidden="true">{section.title}<span>{section.items.length}</span></div>{section.items.map(row)}</div>
      : section.items.map(row)) : <p className="sk-slash-empty">No command or Skill matches “/{token}”.</p>}
  </div> : null;
  // Wired to the textarea so assistive technology follows the active option.
  const inputProps = open && matches.length ? { "aria-controls": listId, "aria-activedescendant": `${listId}-${current}`, "aria-autocomplete": "list" as const } : {};
  return { menu, onKeyDown, inputProps };
}
