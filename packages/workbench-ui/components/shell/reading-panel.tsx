import { useEffect, useRef, useState } from "react";
import { Breadcrumbs, Button, Tooltip } from "@heroui/react";
import { Maximize2, Minimize2, X } from "lucide-react";
import type { ReadingView } from "./reading-context";
import "./reading-panel.css";

/** The reading area's frame: the divider that splits it from the page, its
 * top bar and its scrolling body. What it shows comes from `view`. */
export function ReadingPanel({ view, onClose, fullscreen, onFullscreenChange, onSplitChange, split, body, container }: {
  view: ReadingView; onClose: () => void; fullscreen: boolean;
  onFullscreenChange: (value: boolean) => void; onSplitChange: (value: number) => void;
  split: number; body: HTMLElement | null; container: HTMLElement | null;
}) {
  const [toolbar, setToolbar] = useState<HTMLDivElement | null>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLElement>(null);
  const [dragging, setDragging] = useState(false);
  const [limits, setLimits] = useState({ min: 0.2, max: 0.7 });
  const { title } = view;
  const [location, setLocation] = useState<HTMLDivElement | null>(null);
  useEffect(() => { closeButton.current?.focus(); }, []);
  useEffect(() => {
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented || (event.target instanceof Element && event.target.closest('[role="dialog"], [role="listbox"]'))) return;
      event.preventDefault();
      if (fullscreen) onFullscreenChange(false); else onClose();
    };
    document.addEventListener("keydown", escape);
    return () => document.removeEventListener("keydown", escape);
  }, [fullscreen, onFullscreenChange, onClose]);
  useEffect(() => {
    if (!body || !container) return;
    const measure = () => {
      const width = container.getBoundingClientRect().right - body.getBoundingClientRect().left - 1;
      if (width < 620) return;
      const min = 300 / width, max = 1 - 320 / width;
      setLimits({ min, max });
      onSplitChange(Math.min(max, Math.max(min, split)));
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(container);
    observer.observe(body);
    return () => observer.disconnect();
  }, [body, container, onSplitChange, split]);
  const resize = (value: number) => onSplitChange(Math.min(limits.max, Math.max(limits.min, value)));
  return <>
    <div className="pr-divider" role="separator" tabIndex={0} aria-label="Resize conversation and reading area" aria-orientation="vertical" aria-valuemin={Math.round(limits.min * 100)} aria-valuemax={Math.round(limits.max * 100)} aria-valuenow={Math.round(split * 100)} aria-valuetext={`Conversation ${Math.round(split * 100)}%, reading area ${Math.round((1 - split) * 100)}%`} data-dragging={dragging}
      onPointerDown={event => { if (event.button !== 0) return; event.preventDefault(); event.currentTarget.focus(); event.currentTarget.setPointerCapture(event.pointerId); setDragging(true); }}
      onPointerMove={event => { if (!event.currentTarget.hasPointerCapture(event.pointerId) || !body || !container) return; const left = body.getBoundingClientRect().left; resize((event.clientX - left) / (container.getBoundingClientRect().right - left - 1)); }}
      onPointerUp={event => { if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId); setDragging(false); }}
      onLostPointerCapture={() => setDragging(false)} onDoubleClick={() => resize(1 / 3)}
      onKeyDown={event => { if (!["ArrowLeft", "ArrowRight", "Home", "End", "Enter"].includes(event.key)) return; event.preventDefault(); resize(event.key === "Home" ? limits.min : event.key === "End" ? limits.max : event.key === "Enter" ? 1 / 3 : split + (event.key === "ArrowLeft" ? -0.02 : 0.02)); }} />
    <section ref={panel} className="pr-reader" data-fullscreen={fullscreen} aria-label={`${title} reading area`}>
      {/* The reading area's top bar is the page top bar: same frame, same
          breadcrumbs, same action row. */}
      <header className="p-topbar pr-topbar">
        <div className="p-breadcrumb">
          {/* A reading with a path of its own names it in the same breadcrumbs. */}
          {view.ownLocation ? <div ref={setLocation} className="pr-location-slot" />
            : <Breadcrumbs aria-label="Reading location" className="p-location">
            <Breadcrumbs.Item>{view.label}</Breadcrumbs.Item>
            <Breadcrumbs.Item><span title={title}>{title}</span></Breadcrumbs.Item>
          </Breadcrumbs>}
        </div>
        <div className="p-actions pr-tools"><div ref={setToolbar} className="p-context-actions" />
          <Tooltip><Button isIconOnly size="sm" variant="ghost" aria-label={fullscreen ? "Exit full screen" : "Full screen"} onPress={() => onFullscreenChange(!fullscreen)}>{fullscreen ? <Minimize2 size={16} /> : <Maximize2 size={16} />}</Button><Tooltip.Content>{fullscreen ? "Exit full screen" : "Full screen"}</Tooltip.Content></Tooltip>
          <Tooltip><Button ref={closeButton} isIconOnly size="sm" variant="ghost" aria-label="Close reading area" onPress={onClose}><X size={18} /></Button><Tooltip.Content>Close</Tooltip.Content></Tooltip>
        </div>
      </header>
      <div className="pr-scroll">{view.render({ toolbar, location })}</div>
    </section>
  </>;
}
