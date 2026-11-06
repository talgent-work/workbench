import { useState, type FormEvent } from "react";
import { Alert, Button, Card, FieldError, Label, Skeleton, TextArea, TextField, Tooltip } from "@heroui/react";
import { Info } from "lucide-react";
import { useMyWorkAgent } from "../../lib/agent-appearance-service";
import { MAX_SOUL, soulErrorMessage, useChangeWorkAgentSoul, useWorkAgentSoul } from "../../lib/agent-soul-service";

// Profile › Soul: the persona and working style of the person's own Work Agent.
// When it applies lives in the tooltip beside the title. A Work Agent that
// cannot be loaded is reported once, by Your Work Agent.
export function WorkAgentSoul() {
  const agent = useMyWorkAgent();
  const soul = useWorkAgentSoul(agent.data?.agentId);
  if (agent.isError) return null;
  return (
    <section className="mk-section" aria-labelledby="pf-soul">
      <div className="flex items-center gap-2">
        <h3 id="pf-soul" className="mk-section-title">Soul</h3>
        <Tooltip delay={200}>
          <Tooltip.Trigger aria-label="About Soul" className="rounded-full text-muted"><Info size={15} aria-hidden="true" /></Tooltip.Trigger>
          <Tooltip.Content>Every Work of your Work Agent takes it up at its next start. Running work is not interrupted, and platform rules and the agreements a Work follows still come first.</Tooltip.Content>
        </Tooltip>
      </div>
      {soul.data !== undefined && agent.data ? <SoulEditor key={`${agent.data.agentId}:${soul.data}`} agentId={agent.data.agentId} saved={soul.data} />
        : soul.isError ? (
          <Alert status="danger">
            <Alert.Indicator />
            <Alert.Content><Alert.Title>Your Work Agent's Soul could not be loaded.</Alert.Title></Alert.Content>
            <Button size="sm" variant="secondary" isPending={soul.isFetching} onPress={() => void soul.refetch()}>Retry</Button>
          </Alert>
        ) : (
          <Card className="mk-group" aria-busy="true">
            <Card.Content className="grid gap-3 p-5">
              <p className="sr-only" role="status">Loading your Work Agent's Soul…</p>
              <Skeleton className="h-24 rounded-lg" />
            </Card.Content>
          </Card>
        )}
    </section>
  );
}

function SoulEditor({ agentId, saved }: { agentId: string; saved: string }) {
  const change = useChangeWorkAgentSoul();
  const [draft, setDraft] = useState(saved);
  const soul = draft.trim();
  const problem = Array.from(soul).length > MAX_SOUL ? `Use at most ${MAX_SOUL} characters.` : "";
  const dirty = soul !== saved;
  const save = (event: FormEvent) => {
    event.preventDefault();
    if (!dirty || problem || change.isPending) return;
    change.mutate({ agentId, soul });
  };
  return (
    <Card className="mk-group">
      <form onSubmit={save} aria-label="Soul">
        <Card.Content className="grid gap-4 p-5">
          <TextField value={draft} onChange={setDraft} isInvalid={Boolean(problem)} isDisabled={change.isPending}>
            <Label className="sr-only">Soul</Label>
            <TextArea rows={5} placeholder="How your Work Agent should talk and work" />
            <FieldError>{problem}</FieldError>
          </TextField>
          {change.isError && (
            <Alert status="danger">
              <Alert.Indicator />
              <Alert.Content><Alert.Title>{soulErrorMessage(change.error)}</Alert.Title></Alert.Content>
            </Alert>
          )}
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" isDisabled={!dirty || change.isPending} onPress={() => { change.reset(); setDraft(saved); }}>Discard</Button>
            <Button size="sm" type="submit" isDisabled={!dirty || Boolean(problem)} isPending={change.isPending}>{change.isPending ? "Saving" : "Save"}</Button>
          </div>
        </Card.Content>
      </form>
    </Card>
  );
}
