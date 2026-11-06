import { useLayoutEffect, useRef } from "react";
import type { SlashCandidate } from "./use-slash-completion";
import "./skill-completion.css";

const escape = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

// Renders the text behind a transparent-text textarea so a selected Skill reads
// as a highlighted token in the sentence, with its argument hint as ghost text
// right after a freshly inserted Skill. The textarea stays the only input; the
// mirror copies its box and typography so the two stay aligned in every layout.
const mirrored = ["paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "fontFamily", "fontSize", "fontWeight", "lineHeight", "letterSpacing", "textIndent"] as const;
export function SkillHighlight({ text, candidates }: { text: string; candidates: SlashCandidate[] }) {
  const mirror = useRef<HTMLDivElement>(null);
  const names = candidates.filter(item => !item.builtIn).map(item => item.name);
  const active = names.length > 0;
  useLayoutEffect(() => {
    // The textarea is the mirror's sibling inside the same prompt bar.
    const layer = mirror.current, field = layer?.parentElement?.querySelector("textarea");
    if (!active || !field || !layer) return;
    field.dataset.skillHighlight = "true";
    const sync = () => {
      const style = getComputedStyle(field);
      for (const property of mirrored) layer.style[property] = style[property];
      Object.assign(layer.style, { top: `${field.offsetTop}px`, left: `${field.offsetLeft}px`, width: `${field.clientWidth}px`, height: `${field.clientHeight}px` });
      layer.scrollTop = field.scrollTop;
    };
    sync();
    field.addEventListener("scroll", sync);
    const observer = new ResizeObserver(sync);
    observer.observe(field);
    return () => { field.removeEventListener("scroll", sync); observer.disconnect(); delete field.dataset.skillHighlight; };
  }, [active, text]);
  if (!active) return null;
  const pattern = new RegExp(`(^|\\s)(/(?:${names.map(escape).join("|")}))(?=\\s|$)`, "g");
  const parts: Array<{ text: string; skill: boolean }> = [];
  let last = 0;
  for (const match of text.matchAll(pattern)) {
    const start = match.index + match[1].length;
    parts.push({ text: text.slice(last, start), skill: false }, { text: match[2], skill: true });
    last = start + match[2].length;
  }
  parts.push({ text: text.slice(last), skill: false });
  const fresh = /\/([\w:.-]+) $/.exec(text)?.[1];
  const hint = fresh ? candidates.find(item => item.name === fresh && !item.builtIn)?.argumentHint : "";
  return <div ref={mirror} className="sk-mirror" aria-hidden="true">
    {parts.map((part, index) => part.skill ? <mark key={index} className="sk-mark">{part.text}</mark> : <span key={index}>{part.text}</span>)}
    {hint && <span className="sk-ghost">{hint}</span>}{"​"}
  </div>;
}
