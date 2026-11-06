import { Card, TextArea } from "@heroui/react";
import { useImperativeHandle, useLayoutEffect, useRef, type ComponentProps, type ReactNode } from "react";
import "./work-start.css";

interface PromptBarProps {
  input: ComponentProps<typeof TextArea>;
  leading: ReactNode;
  children: ReactNode;
  context?: ReactNode;
  inline?: boolean;
}

export function PromptBar({ input, leading, children, context, inline = false }: PromptBarProps) {
  const textarea = useRef<HTMLTextAreaElement>(null);
  useImperativeHandle(input.ref, () => textarea.current!);
  useLayoutEffect(() => {
    if (!inline || !textarea.current) return;
    const field = textarea.current;
    const bar = field.closest<HTMLElement>(".wpb-bar")!;
    const resize = () => {
      bar.dataset.expanded = "false";
      field.style.overflowY = "hidden";
      field.style.height = "auto";
      const lineHeight = Number.parseFloat(getComputedStyle(field).lineHeight);
      bar.dataset.expanded = String(Boolean(field.value) && field.scrollHeight > Math.ceil(lineHeight));
      // scrollHeight rounds fractional line heights; leave room for the last line.
      const height = field.value ? field.scrollHeight + 1 : Math.ceil(lineHeight);
      field.style.height = `${Math.min(height, 220)}px`;
      field.style.overflowY = height > 220 ? "auto" : "hidden";
    };
    resize();
    let width = bar.clientWidth;
    const observer = new ResizeObserver(() => {
      if (bar.clientWidth !== width) { width = bar.clientWidth; resize(); }
    });
    observer.observe(bar);
    return () => observer.disconnect();
  }, [inline, input.value]);

  return <Card className={`wpb-bar${inline ? " wpb-bar-compact" : ""}`}>
    <TextArea {...input} ref={textarea} className="wpb-input resize-none" rows={inline ? 1 : 3} />
    {context}
    <div className="wpb-toolbar">{leading}{children}</div>
  </Card>;
}
