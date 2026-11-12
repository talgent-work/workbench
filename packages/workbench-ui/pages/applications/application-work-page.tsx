import { useCallback, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { PageLayout } from "../../components/shell/page-layout";
import { ConversationUnavailable } from "../../components/work/conversation-unavailable";
import { WorkConversation } from "../../components/work/work-conversation";
import { useOwnedApplications } from "../../lib/application-service";
import { useApplicationWorks } from "../../lib/application-work-service";
import type { SubAgent } from "../../lib/subagents";
import { StandaloneWorkContext } from "../workbench/standalone-work-context";

/** One of an Application's Works as its Application Owner reads it: its
 * message stream and its files, never driven from here. */
export function ApplicationWorkPage() {
  const { workId = "" } = useParams();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const applicationId = params.get("app") ?? "";
  const application = useOwnedApplications().data?.find(item => item.id === applicationId);
  const listed = useApplicationWorks(applicationId);
  const work = listed.data?.find(item => item.id === workId);
  const title = work?.title ?? "Work";
  const [live, setLive] = useState<{ workId: string; lifecycle: string }>({ workId: "", lifecycle: "" });
  const onLifecycle = useCallback((lifecycle: string) => setLive({ workId, lifecycle }), [workId]);
  const [liveSubAgents, setLiveSubAgents] = useState<{ workId: string; agents: SubAgent[] }>();
  const onSubAgents = useCallback((agents: SubAgent[]) => setLiveSubAgents({ workId, agents }), [workId]);
  const [withdrawn, setWithdrawn] = useState<string | null>(null);
  const lifecycle = (live.workId === workId ? live.lifecycle : "") || work?.state || "";
  const section = (id: string) => navigate(`/applications?app=${encodeURIComponent(applicationId)}&section=${id}`);
  const trail = [
    { label: application?.name ?? "Application", onPress: () => section("overview") },
    { label: "Works", onPress: () => section("works") },
  ];

  return <PageLayout workspaceTitle={title} trail={trail}>
    <div className="agent-grid">
      {withdrawn === workId
        ? <section data-testid="agent-stream-panel" className="stream-panel" aria-label="Agent Stream"><ConversationUnavailable withdrawn /></section>
        : <WorkConversation
          key={workId}
          agentWorkId={workId}
          label={title}
          readOnly
          onAccessWithdrawn={() => setWithdrawn(workId)}
          workLifecycle={lifecycle}
          isWorkLifecyclePending={!lifecycle}
          onWorkLifecycleChange={onLifecycle}
          onSubAgentsChange={onSubAgents}
        />}
      {withdrawn !== workId && <StandaloneWorkContext workId={workId} title={title} owner={false} withFiles workLifecycle={lifecycle} workCreatedAt={null}
        progress={undefined} subAgents={liveSubAgents?.workId === workId ? liveSubAgents.agents : undefined} />}
    </div>
  </PageLayout>;
}
