import { useEffect, useState } from "react";
import { Button, Card, Tooltip } from "@heroui/react";
import { Check, Copy } from "lucide-react";
import { LoadState } from "../../components/marketplace/marketplace-shared";
import { installCommands, useComputerRelease } from "../../lib/computer-release";

export function InstallCommands() {
  const release = useComputerRelease();
  return <section className="mk-section" aria-labelledby="cp-install">
    <h3 id="cp-install" className="mk-section-title">Add a computer</h3>
    <p className="text-[13px] text-muted">Run these on the machine: macOS, Linux or Windows. Needs Node.js 24 or newer.</p>
    <LoadState noun="the install command" isPending={release.isPending} isError={release.isError} hasData={release.data !== undefined} isFetching={release.isFetching} onRetry={() => void release.refetch()} />
    {release.data !== undefined && <Card className="mk-group">
      <ol className="mk-group-list">
        {installCommands(window.location.origin, release.data).map(command => <li key={command} className="!items-center"><code className="min-w-0 flex-1 break-all">{command}</code><CopyButton text={command} /></li>)}
      </ol>
    </Card>}
    {release.data !== undefined && <p className="text-[13px] text-muted">The last command keeps it running in the background from each login. To run it in a terminal instead, use <code>talgent-computer run</code>.</p>}
    {release.data !== undefined && <p className="text-[13px] text-muted">On Windows, use Command Prompt, or allow npm’s commands in PowerShell first with <code>Set-ExecutionPolicy -Scope CurrentUser RemoteSigned</code>.</p>}
  </section>;
}

function CopyButton({ text }: { text: string }) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  useEffect(() => {
    if (state === "idle") return;
    const timer = window.setTimeout(() => setState("idle"), 2000);
    return () => window.clearTimeout(timer);
  }, [state]);
  const label = state === "copied" ? "Copied" : state === "failed" ? "Couldn’t copy" : "Copy command";
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
