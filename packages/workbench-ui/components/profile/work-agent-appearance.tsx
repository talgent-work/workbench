import { Alert, Button, Card, Skeleton } from "@heroui/react";
import { AgentAppearanceEditor } from "../agent/agent-appearance-editor";
import { agentAppearanceErrorMessage, useChangeMyWorkAgentAppearance, useMyWorkAgent, type MyWorkAgent } from "../../lib/agent-appearance-service";

// Profile › Your Work Agent: the person names their Work Agent and picks its
// icon. Every place the Agent appears shows what is saved here.
export function WorkAgentAppearance() {
  const agent = useMyWorkAgent();
  return (
    <section className="mk-section" aria-labelledby="pf-agent">
      <h3 id="pf-agent" className="mk-section-title">Your Work Agent</h3>
      {agent.data ? <AppearanceEditor key={`${agent.data.agentId}:${agent.data.name}:${agent.data.icon.glyph}:${agent.data.icon.color}`} saved={agent.data} />
        : agent.isError ? (
          <Alert status="danger">
            <Alert.Indicator />
            <Alert.Content>
              <Alert.Title>Your Work Agent could not be loaded.</Alert.Title>
              <Alert.Description>Its name and icon stay as they were.</Alert.Description>
            </Alert.Content>
            <Button size="sm" variant="secondary" isPending={agent.isFetching} onPress={() => void agent.refetch()}>Retry</Button>
          </Alert>
        ) : (
          <Card className="mk-group" aria-busy="true">
            <Card.Content className="flex items-center gap-4 p-5">
              <p className="sr-only" role="status">Loading your Work Agent…</p>
              <Skeleton className="size-14 rounded-2xl" />
              <div className="grid flex-1 gap-2"><Skeleton className="h-3 w-16 rounded" /><Skeleton className="h-9 rounded-lg" /></div>
            </Card.Content>
          </Card>
        )}
    </section>
  );
}

function AppearanceEditor({ saved }: { saved: MyWorkAgent }) {
  const change = useChangeMyWorkAgentAppearance();
  return <AgentAppearanceEditor saved={saved} label="Your Work Agent" hint="Shown wherever your Work Agent appears, including comments it wrote before."
    saving={change.isPending} error={change.isError ? agentAppearanceErrorMessage(change.error) : ""}
    onSave={appearance => change.mutate({ ...saved, ...appearance })} onDiscard={() => change.reset()} />;
}
