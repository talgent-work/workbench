// PROTOTYPE ONLY: cycles ?variant= between a prototype's variants. Hidden in
// production builds.
import { useEffect } from "react";
import { useSearchParams } from "react-router-dom";
import { ChevronLeft, ChevronRight } from "lucide-react";

export function usePrototypeVariant(keys: string[]): string {
  const [params] = useSearchParams();
  const current = params.get("variant") ?? keys[0];
  return keys.includes(current) ? current : keys[0];
}

export function PrototypeSwitcher({ variants }: { variants: { key: string; name: string }[] }) {
  const [params, setParams] = useSearchParams();
  const keys = variants.map(variant => variant.key);
  const current = usePrototypeVariant(keys);
  const index = keys.indexOf(current);
  const go = (step: number) => {
    const next = keys[(index + step + keys.length) % keys.length];
    const updated = new URLSearchParams(params);
    updated.set("variant", next);
    setParams(updated, { replace: true });
  };
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, [contenteditable]")) return;
      if (event.key === "ArrowLeft") go(-1);
      if (event.key === "ArrowRight") go(1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
  if (import.meta.env.PROD) return null;
  return (
    <div style={{ position: "fixed", bottom: 20, left: "50%", transform: "translateX(-50%)", zIndex: 9999, display: "flex", alignItems: "center", gap: 8, padding: "6px 10px", borderRadius: 999, background: "#111", color: "#fff", boxShadow: "0 6px 24px rgba(0,0,0,.35)", fontSize: 13, fontFamily: "system-ui" }}>
      <button type="button" aria-label="Previous variant" onClick={() => go(-1)} style={{ color: "inherit", display: "flex" }}><ChevronLeft size={18} /></button>
      <span>PROTOTYPE · {current} ({variants[index]?.name})</span>
      <button type="button" aria-label="Next variant" onClick={() => go(1)} style={{ color: "inherit", display: "flex" }}><ChevronRight size={18} /></button>
    </div>
  );
}
