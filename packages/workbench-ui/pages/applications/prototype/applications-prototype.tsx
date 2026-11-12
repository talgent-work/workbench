// PROTOTYPE (Applications): two layouts for the Application pages on one route,
// switched by ?variant= and a floating bar. Throwaway: the verdict goes to
// the issue and this branch is never merged.
import { useEffect } from "react";
import { Button, Kbd } from "@heroui/react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import { ProtoStoreProvider } from "./proto-store";
import { VariantB } from "./variant-b";
import { VariantC } from "./variant-c";
import "../../../components/marketplace/marketplace.css";

const variants = [
  { id: "B", name: "Tabs", Page: VariantB },
  { id: "C", name: "Works first", Page: VariantC },
] as const;

export function ApplicationsPrototype() {
  const [params, setParams] = useSearchParams();
  const index = Math.max(0, variants.findIndex(variant => variant.id === params.get("variant")));
  const current = variants[index];
  const go = (step: number) => setParams(current => { const next = new URLSearchParams(current); next.set("variant", variants[(index + step + variants.length) % variants.length].id); return next; }, { replace: true });
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, [contenteditable=true], [role=listbox], [role=grid], [role=tablist]")) return;
      if (event.key === "ArrowLeft") go(-1);
      if (event.key === "ArrowRight") go(1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
  return <ProtoStoreProvider>
    <current.Page key={current.id} />
    {import.meta.env.DEV && <div className="fixed bottom-4 left-1/2 z-50 flex -translate-x-1/2 items-center gap-2 rounded-full border border-separator bg-overlay px-2 py-1.5 shadow-lg" role="toolbar" aria-label="Prototype variants">
      <Button size="sm" variant="ghost" isIconOnly aria-label="Previous variant" onPress={() => go(-1)}><ChevronLeft size={16} /></Button>
      <span className="min-w-44 text-center text-[13px]"><strong>{current.id}</strong> · {current.name} <span className="text-muted">({index + 1}/{variants.length})</span></span>
      <Button size="sm" variant="ghost" isIconOnly aria-label="Next variant" onPress={() => go(1)}><ChevronRight size={16} /></Button>
      <span className="hidden text-[12px] text-muted sm:inline"><Kbd>←</Kbd> <Kbd>→</Kbd></span>
    </div>}
  </ProtoStoreProvider>;
}
