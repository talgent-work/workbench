import { useEffect, useState } from "react";
import { Button, Tooltip } from "@heroui/react";
import { Check, Copy } from "lucide-react";

/** Copies text, naming what it copies ("command", "client ID"), and says
 * whether it worked, for sight and for assistive technology. */
export function CopyButton({ text, what }: { text: string; what: string }) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  useEffect(() => {
    if (state === "idle") return;
    const timer = window.setTimeout(() => setState("idle"), 2000);
    return () => window.clearTimeout(timer);
  }, [state]);
  const label = state === "copied" ? "Copied" : state === "failed" ? "Couldn’t copy" : `Copy ${what}`;
  const copy = () => {
    const clipboard = navigator.clipboard as Clipboard | undefined;
    if (!clipboard) { setState("failed"); return; }
    clipboard.writeText(text).then(() => setState("copied"), () => setState("failed"));
  };
  return <><Tooltip delay={300}>
    <Button size="sm" variant="ghost" isIconOnly aria-label={label} onPress={copy}>
      {state === "copied" ? <Check size={15} aria-hidden="true" /> : <Copy size={15} aria-hidden="true" />}
    </Button>
    <Tooltip.Content>{label}</Tooltip.Content>
  </Tooltip><span className="sr-only" role="status">{state === "idle" ? "" : label}</span></>;
}
