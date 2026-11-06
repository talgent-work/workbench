import { Card } from "@heroui/react";
import { Monitor } from "lucide-react";
import { LoadState } from "../../components/marketplace/marketplace-shared";
import { PageLayout } from "../../components/shell/page-layout";
import { presenceLabel, roomLabel, useComputers, type ManagedComputer } from "../../lib/computer-service";
import { useNow } from "../../lib/use-now";
import { InstallCommands } from "./install-commands";
import { RemoveComputerButton } from "./remove-computer-dialog";
import { RenameComputerButton } from "./rename-computer-dialog";
import { RoomRulesButton } from "./room-rules-dialog";
import "../../components/marketplace/marketplace.css";

/** Computers: the person's Managed Computers with their Presence, and how to
 * add another. A Cloud is not one of them and never shows here. */
export function ComputersPage() {
  const computers = useComputers();
  const list = computers.data ?? [];
  return <PageLayout workspaceTitle="Computers">
    <div className="mx-auto grid w-[min(640px,100%)] gap-8 py-6">
      <section className="mk-section" aria-labelledby="cp-list">
        <h3 id="cp-list" className="mk-section-title">Your computers{list.length > 0 && <span className="mk-count">{list.length}</span>}</h3>
        <LoadState noun="your computers" isPending={computers.isPending} isError={computers.isError} hasData={Boolean(computers.data)} isFetching={computers.isFetching} onRetry={() => void computers.refetch()} />
        {computers.data && (list.length === 0
          ? <p className="text-[14px] text-muted">No computers yet. Install Talgent Computer on a machine to run Works there.</p>
          : <ComputerList computers={list} />)}
      </section>
      <InstallCommands />
    </div>
  </PageLayout>;
}

function ComputerList({ computers }: { computers: ManagedComputer[] }) {
  const now = useNow(30_000);
  return <Card className="mk-group">
    <ul className="mk-group-list" aria-label="Your computers">
      {computers.map(computer => {
        const online = computer.presence.kind === "online";
        return <li key={computer.id} className="!items-center">
          <Monitor size={18} className={online ? "shrink-0" : "shrink-0 text-muted"} aria-hidden="true" />
          <span className="mk-row-text min-w-0 flex-1">
            <strong className="truncate">{computer.name}</strong>
            <span>
              <span className={online ? "text-success" : undefined}>{presenceLabel(computer.presence, now)}</span>
              {computer.talgentComputerVersion && <> · Talgent Computer {computer.talgentComputerVersion}</>}
              {computer.claudeCodeVersion && <> · Claude Code {computer.claudeCodeVersion}</>}
            </span>
            <span>{roomLabel(computer)} · {computer.policy === "queue" ? "Queues runs when full" : "Rejects runs when full"}</span>
          </span>
          <RoomRulesButton computer={computer} />
          <RenameComputerButton computer={computer} />
          <RemoveComputerButton computer={computer} />
        </li>;
      })}
    </ul>
  </Card>;
}
