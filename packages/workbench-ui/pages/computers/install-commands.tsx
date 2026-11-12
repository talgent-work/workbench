import { Card } from "@heroui/react";
import { CopyButton } from "../../components/copy-button";
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
        {installCommands(window.location.origin, release.data).map(command => <li key={command} className="!items-center"><code className="min-w-0 flex-1 break-all">{command}</code><CopyButton text={command} what="command" /></li>)}
      </ol>
    </Card>}
    {release.data !== undefined && <p className="text-[13px] text-muted">The last command keeps it running in the background from each login. To run it in a terminal instead, use <code>talgent-computer run</code>.</p>}
    {release.data !== undefined && <p className="text-[13px] text-muted">On Windows, use Command Prompt, or allow npm’s commands in PowerShell first with <code>Set-ExecutionPolicy -Scope CurrentUser RemoteSigned</code>.</p>}
  </section>;
}
