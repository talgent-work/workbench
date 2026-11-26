import { useState, type ReactNode } from "react";
import { Alert, Button, Card, Skeleton, Tabs } from "@heroui/react";
import { AgentTextEditor } from "../agent/agent-text-editor";
import { useMyWorkAgent } from "../../lib/agent-appearance-service";
import { MAX_SOUL, soulErrorMessage, useChangeWorkAgentSoul, useWorkAgentSoul } from "../../lib/agent-soul-service";
import {
  MAX_WORKSPACE_INSTRUCTIONS, useChangeWorkAgentWorkspaceInstructions, useWorkAgentWorkspaceInstructions, workspaceInstructionsErrorMessage,
} from "../../lib/workspace-instructions-service";

type Guidance = "soul" | "instructions";

// Both texts are edited in boxes of this height, so switching tabs moves nothing.
const height = "15rem";
// A panel's content lines up with the tab list above it.
const panel = "px-0 pb-0 pt-4";

// Profile › How your Work Agent works: its Soul and its Workspace
// Instructions, the two texts the person writes for every Work of their Work
// Agent, edited in boxes of one height. Drafts live here, so switching tabs
// keeps an unsaved one, which its tab marks. A Work Agent that cannot be
// loaded is reported once, by Your Work Agent.
export function WorkAgentGuidance() {
  const agent = useMyWorkAgent();
  const agentId = agent.data?.agentId;
  const soul = useWorkAgentSoul(agentId);
  const instructions = useWorkAgentWorkspaceInstructions(agentId);
  const changeSoul = useChangeWorkAgentSoul();
  const changeInstructions = useChangeWorkAgentWorkspaceInstructions();
  const [selected, setSelected] = useState<Guidance>("soul");
  const [drafts, setDrafts] = useState<Partial<Record<Guidance, string>>>({});
  if (agent.isError) return null;
  const draft = (kind: Guidance, saved: string) => drafts[kind] ?? saved;
  const unsaved = (kind: Guidance, saved: string | undefined) => saved !== undefined && drafts[kind] !== undefined && drafts[kind].trim() !== saved;
  const edit = (kind: Guidance) => (value: string) => setDrafts(current => ({ ...current, [kind]: value }));
  const settle = (kind: Guidance) => setDrafts(current => ({ ...current, [kind]: undefined }));
  return (
    <section className="mk-section" aria-labelledby="pf-guidance">
      <h3 id="pf-guidance" className="mk-section-title">How your Work Agent works</h3>
      <Card className="mk-group">
        <Card.Content className="grid gap-4 p-5">
          <Tabs selectedKey={selected} onSelectionChange={key => setSelected(key as Guidance)}>
            <Tabs.ListContainer>
              <Tabs.List aria-label="How your Work Agent works">
                <Tabs.Tab id="soul">Soul<UnsavedMark shown={unsaved("soul", soul.data)} /><Tabs.Indicator /></Tabs.Tab>
                <Tabs.Tab id="instructions">Workspace instructions<UnsavedMark shown={unsaved("instructions", instructions.data)} /><Tabs.Indicator /></Tabs.Tab>
              </Tabs.List>
            </Tabs.ListContainer>
            <Tabs.Panel id="soul" className={panel}>
              <Loaded noun="Soul" query={soul}>
                {saved => (
                  <AgentTextEditor label="Soul" height={height} draft={draft("soul", saved)} saved={saved} max={MAX_SOUL} placeholder="How your Work Agent should talk and work"
                    saving={changeSoul.isPending} error={changeSoul.isError ? soulErrorMessage(changeSoul.error) : ""} onChange={edit("soul")}
                    onSave={text => changeSoul.mutate({ agentId: agentId ?? "", soul: text }, { onSuccess: () => settle("soul") })}
                    onDiscard={() => { changeSoul.reset(); settle("soul"); }} />
                )}
              </Loaded>
            </Tabs.Panel>
            <Tabs.Panel id="instructions" className={panel}>
              <Loaded noun="workspace instructions" query={instructions}>
                {saved => (
                  <AgentTextEditor label="Workspace instructions" document height={height} draft={draft("instructions", saved)} saved={saved} max={MAX_WORKSPACE_INSTRUCTIONS}
                    placeholder={"# Working in these repositories\n- Run the tests before you push\n- Keep commits small"}
                    saving={changeInstructions.isPending} error={changeInstructions.isError ? workspaceInstructionsErrorMessage(changeInstructions.error) : ""} onChange={edit("instructions")}
                    onSave={text => changeInstructions.mutate({ agentId: agentId ?? "", instructions: text }, { onSuccess: () => settle("instructions") })}
                    onDiscard={() => { changeInstructions.reset(); settle("instructions"); }} />
                )}
              </Loaded>
            </Tabs.Panel>
          </Tabs>
        </Card.Content>
      </Card>
    </section>
  );
}

// A dot after a tab's name while that tab holds an unsaved draft.
function UnsavedMark({ shown }: { shown: boolean }) {
  if (!shown) return null;
  return <>{" "}<span aria-hidden="true" className="ml-1 inline-block size-1.5 rounded-full bg-accent align-middle" /><span className="sr-only">(unsaved)</span></>;
}

// One text as its query loads it: the editor once read, a retry when reading
// failed, a placeholder until then.
function Loaded({ noun, query, children }: {
  noun: string; query: { data: string | undefined; isError: boolean; isFetching: boolean; refetch: () => unknown }; children: (saved: string) => ReactNode;
}) {
  if (query.data !== undefined) return children(query.data);
  if (query.isError) return (
    <Alert status="danger">
      <Alert.Indicator />
      <Alert.Content><Alert.Title>Your Work Agent's {noun} could not be loaded.</Alert.Title></Alert.Content>
      <Button size="sm" variant="secondary" isPending={query.isFetching} onPress={() => void query.refetch()}>Retry</Button>
    </Alert>
  );
  return <div aria-busy="true"><p className="sr-only" role="status">Loading your Work Agent's {noun}…</p><Skeleton className="h-24 rounded-lg" /></div>;
}
