import { useWorkComputer } from "../../lib/computer-service";
import { useCallback, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { StartupProgress } from "@talgent/workbench-contracts/agent/public/v1/startup_pb";
import { useParams } from "react-router-dom";
import { PageLayout } from "../../components/shell/page-layout";
import { ConversationUnavailable } from "../../components/work/conversation-unavailable";
import { WorkConversation } from "../../components/work/work-conversation";
import { WorkSnapshotUnavailable } from "../../components/work/work-snapshot-state";
import { useCurrentUser } from "../../lib/account-service";
import { useAgentWork } from "../../lib/agent-work-service";
import { useLibraryWork, usePersonalWorks } from "../../lib/personal-work-service";
import { newestStartupProgress } from "../../lib/startup-progress";
import type { SubAgent } from "../../lib/subagents";
import { useUploadWorkAttachment } from "../../lib/work-file-service";
import { adaptWorkProgress } from "../../lib/work-progress";
import { PersonalWorkContext } from "./personal-work-context";
import { ShareButton } from "./share-dialog";
import { TitleButton } from "./title-dialog";

/** A Personal Work: its conversation and context card, titled by the Work.
 * Its owner continues, renames and shares it; a Work Share holder reads it. */
export function PersonalWorkPage() {
  const { workId = "" } = useParams();
  const userId = useCurrentUser().data?.user?.id ?? "";
  const snapshot = useAgentWork(workId);
  const work = snapshot.data?.work;
  const computer = useWorkComputer(work?.computerId, Boolean(work?.permissionMode));
  const refused = snapshot.error ? ConnectError.from(snapshot.error).code : undefined;
  // Agent shows a Work only to its owner; anyone else may still hold a Work Share.
  const reader = refused === Code.PermissionDenied;
  const own = usePersonalWorks();
  const shared = useLibraryWork(workId, reader);
  // Only a Personal Work has a title of its own and is shared from here; a
  // Work of a Project is the Project's to share.
  const personal = own.data?.find(item => item.id === workId);
  // A Work with a Project keeps its files in the Project's spaces, not here.
  const personalWork = work ? Boolean(personal) : Boolean(shared.data?.personal);
  const title = (work ? personal?.title : shared.data?.title) || (reader ? "Shared work" : "Work");
  const [live, setLive] = useState<{ workId: string; lifecycle: string; startup?: StartupProgress }>({ workId: "", lifecycle: "" });
  const onLifecycle = useCallback((lifecycle: string, _updatedAt?: Date, startup?: StartupProgress) => setLive({ workId, lifecycle, startup }), [workId]);
  const [liveSubAgents, setLiveSubAgents] = useState<{ workId: string; agents: SubAgent[] }>();
  const onSubAgents = useCallback((agents: SubAgent[]) => setLiveSubAgents({ workId, agents }), [workId]);
  const [withdrawn, setWithdrawn] = useState<string | null>(null);
  const upload = useUploadWorkAttachment(workId);
  const liveLifecycle = live.workId === workId ? live.lifecycle : "";
  const workLifecycle = liveLifecycle || work?.state || "";
  const startupProgress = newestStartupProgress(live.workId === workId ? live.startup : undefined, work?.startupProgress);
  const pending = snapshot.isPending || (reader && (shared.isPending || !liveLifecycle));
  const starting = workLifecycle !== "closed" && (pending || Boolean(startupProgress ? startupProgress.status !== "completed" : ["created", "starting"].includes(workLifecycle)));
  const refresh = useCallback(() => { void snapshot.refetch(); }, [snapshot]);

  // Refused by Agent and not in the person's Library: no Work Share to read it by.
  const unavailable = withdrawn === workId ? <ConversationUnavailable withdrawn />
    : reader && shared.data === null ? <ConversationUnavailable />
    : refused === Code.NotFound ? <WorkSnapshotUnavailable notFound onRetry={refresh} />
    : snapshot.isError && !reader ? <WorkSnapshotUnavailable notFound={false} onRetry={refresh} />
    : null;
  return <PageLayout workspaceTitle={title} topActions={work && personal && <>
    <TitleButton workId={workId} title={title} />
    <ShareButton workId={workId} ownerId={userId} readerIds={work.shareReaderIds} />
  </>}>
    <div className="agent-grid">
      {unavailable ? <section data-testid="agent-stream-panel" className="stream-panel" aria-label="Agent Stream">{unavailable}</section>
        : (work || (reader && !shared.isPending)) && <WorkConversation
          key={workId}
          agentWorkId={workId}
          label={title}
          readOnly={reader}
          onAccessWithdrawn={() => setWithdrawn(workId)}
          workLifecycle={workLifecycle}
          stateReason={work?.stateReason}
          disconnectedSince={work?.disconnectedSince ? timestampDate(work.disconnectedSince) : undefined}
          resumeOnReconnection={work?.resumeOnReconnection}
          isWorkLifecyclePending={pending && !liveLifecycle}
          startupProgress={startupProgress}
          startupRequest={work?.startupRequest}
          workModel={work?.model}
          workModelOption={work?.modelOption}
          permissionMode={work?.permissionMode}
          computer={computer}
          failureReason={work?.diagnosticReason}
          sandbox={work?.sandbox}
          allowedActions={work?.allowedActions}
          deskWaiting={work?.deskWaiting}
          workVersion={work?.version}
          workControlPending={Boolean(work?.archivePending || work?.resumePending)}
          onWorkControlChange={refresh}
          onWorkLifecycleChange={onLifecycle}
          onSubAgentsChange={onSubAgents}
          onUploadAttachment={work && personalWork ? file => upload.mutateAsync({ file }) : undefined}
        />}
      {!unavailable && !snapshot.isPending && !starting && <PersonalWorkContext workId={workId} title={title} owner={Boolean(work)} withFiles={personalWork} workLifecycle={workLifecycle}
        workCreatedAt={work?.createdAt ? timestampDate(work.createdAt) : null} progress={work ? adaptWorkProgress(work.workProgress) : undefined}
        subAgents={liveSubAgents?.workId === workId ? liveSubAgents.agents : undefined} />}
      {snapshot.isPending && <span className="sr-only" role="status">Loading work…</span>}
    </div>
  </PageLayout>;
}
