import { useState } from "react";
import { Alert, Button, Card, Modal, Separator, Typography } from "@heroui/react";
import type { Connector } from "@talgent/contracts/marketplace/public/v1/connectors_pb";
import { Clock, Globe, KeyRound, Plug, SquareTerminal, Store } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { draftOf, draftProblems, emptyDraft, providerHosts, refusal, refusedField, useConnectorAuthoring, useConnectorIcon, useOwnedConnectors, type ConnectorDraft, type DraftProblems, type IconImage } from "../../lib/connector-workshop";
import { useConnectors } from "../../lib/marketplace-catalog";
import { useProviders } from "../../lib/marketplace-connections";
import { marketplacePaths, workshopPaths } from "../../lib/marketplace-routes";
import { notify } from "../../lib/notifications";
import { EditableConnectorIcon } from "../marketplace/connector-image";
import { ConfirmDialog, LoadState } from "../marketplace/marketplace-shared";
import { PageLayout } from "../shell/page-layout";
import { AboutFields, AddressField, NameField, ProviderField, TransportField } from "./connector-fields";
import { DangerRow, Fact, SaveBar, StateChip } from "./workshop-parts";
import "../marketplace/marketplace.css";

const dateOf = (millis: bigint) => new Date(Number(millis)).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });

/** One Workshop item on one page, or the same page adding a new one. */
export function WorkshopItem({ connectorId }: { connectorId?: string }) {
  const navigate = useNavigate();
  const owned = useOwnedConnectors();
  const trail = [{ label: "Workshop", onPress: () => navigate(workshopPaths.list) }];
  if (!connectorId) return <ItemPage key="new" />;
  const connector = owned.data?.find(item => item.connectorId === connectorId);
  if (connector) return <ItemPage key={connectorId} connector={connector} />;
  return <PageLayout workspaceTitle={owned.data ? "Not found" : "Connector"} trail={trail}><div className="mx-auto w-[min(560px,100%)] p-8">
    <LoadState noun="connector" isPending={owned.isPending} isError={owned.isError} hasData={false} isFetching={owned.isFetching} onRetry={() => void owned.refetch()} />
    {owned.data && <Card><Card.Header><Card.Title>Nothing here</Card.Title><Card.Description>It was deleted, or it isn’t yours.</Card.Description></Card.Header>
      <Card.Footer><Button size="sm" onPress={() => navigate(workshopPaths.list)}>Back to your workshop</Button></Card.Footer></Card>}
  </div></PageLayout>;
}

function ItemPage({ connector }: { connector?: Connector }) {
  const navigate = useNavigate();
  const owned = useOwnedConnectors();
  const providers = useProviders().data ?? [];
  const catalog = useConnectors();
  const authoring = useConnectorAuthoring();
  const savedIcon = useConnectorIcon(connector ?? { connectorId: "", iconDigest: "" });
  const [draft, setDraft] = useState<ConnectorDraft>(connector ? draftOf(connector) : emptyDraft);
  const [touched, setTouched] = useState(false);
  const [refused, setRefused] = useState<DraftProblems>({});
  const [confirm, setConfirm] = useState<"publish" | "withdraw" | "delete" | null>(null);
  const back = () => navigate(workshopPaths.list);
  const takenNames = (owned.data ?? []).map(item => item.name);
  const problems = { ...draftProblems(draft, { adding: !connector, providers, takenNames }), ...refused };
  const set = (patch: Partial<ConnectorDraft>) => { setDraft(current => ({ ...current, ...patch })); setRefused({}); };
  const saved = connector ? draftOf(connector) : emptyDraft;
  const dirty = (["url", "command", "description", "category"] as const).some(field => draft[field] !== saved[field]);
  const provider = providers.find(item => item.name === (connector ? connector.provider : draft.provider));
  const categories = [...new Set([...(catalog.data ?? []), ...(owned.data ?? [])].map(item => item.category).filter(Boolean))].sort();
  const failed = (what: string) => (error: unknown) => notify(`workshop:${what}`, "danger", `${draft.name || "The connector"} couldn’t be ${what}`, refusal(error));

  // A refusal that names a field shows under it; any other is a toast.
  const refuseInline = (otherwise: (error: unknown) => void) => (error: unknown) => {
    const field = refusedField(error), message = refusal(error);
    if (field) setRefused({ [field]: message.charAt(0).toUpperCase() + message.slice(1) + "." });
    else otherwise(error);
  };
  const save = () => {
    setTouched(true);
    if (Object.keys(problems).length) return;
    if (!connector) {
      authoring.add.mutate(draft, {
        onSuccess: added => {
          notify(`workshop:add:${added.connectorId}`, "success", `Added ${added.name}`, "Only you see it. Install it from the Marketplace to try it.");
          navigate(workshopPaths.connector(added.connectorId), { replace: true });
        },
        onError: refuseInline(failed("added")),
      });
      return;
    }
    authoring.change.mutate({ connector, draft }, {
      onSuccess: saved => {
        // The saved form, so a line quoted differently is no longer a change.
        setDraft(draftOf(saved));
        notify(`workshop:save:${connector.connectorId}`, "success", "Changes saved", "Works started from now on use them.");
      },
      onError: refuseInline(failed("saved")),
    });
  };
  // The icon is saved as soon as it is chosen; a new item keeps it until it is added.
  const changeIcon = (icon?: IconImage) => {
    if (!connector) return set({ icon });
    authoring.changeIcon.mutate({ connector, icon }, {
      onSuccess: () => notify(`workshop:icon:${connector.connectorId}`, "success", icon ? "Icon updated" : "Icon removed"),
      onError: failed("changed"),
    });
  };
  // The outcome is awaited rather than left to per-call callbacks, which
  // never run once a deleted Connector's page has left.
  type Command = { mutateAsync: (connector: Connector) => Promise<unknown> };
  const run = (mutation: Command, what: string, toast: [string, string?], after?: () => void) => {
    if (!connector) return;
    mutation.mutateAsync(connector).then(() => { notify(`workshop:${what}:${connector.connectorId}`, "success", ...toast); after?.(); }, failed(what));
  };

  const showForm = !connector || connector.state !== "withdrawn";
  return <PageLayout workspaceTitle={connector ? connector.name : "New connector"} trail={[{ label: "Workshop", onPress: back }]}>
    <div className="mx-auto grid w-[min(860px,100%)] gap-8 px-4 pb-16 pt-8">
      <header className="flex flex-wrap items-center gap-5">
        <EditableConnectorIcon name={draft.name} url={connector ? savedIcon.data || undefined : draft.icon?.url} onChange={changeIcon} isPending={authoring.changeIcon.isPending} />
        <div className="grid min-w-0 flex-1 gap-2">
          <Typography.Heading level={1} className="truncate text-[26px] tracking-tight">{connector ? connector.name : draft.name || "New connector"}</Typography.Heading>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px] text-muted">
            {connector ? <StateChip state={connector.state} /> : <StateChip state="Not added yet" />}
            {connector && (connector.transport === "stdio" ? <Fact icon={SquareTerminal}>Local command</Fact> : <>
              <Fact icon={Plug}>Remote server</Fact>
              {provider && <Fact icon={KeyRound}>{provider.displayName} sign-in</Fact>}
              {provider && <Fact icon={Globe}>{providerHosts(provider).join(", ")}</Fact>}
            </>)}
            {connector && connector.publishedAtUnixMs > 0n && <Fact icon={Clock}>Published {dateOf(connector.publishedAtUnixMs)}</Fact>}
          </div>
        </div>
        {connector && connector.state !== "withdrawn" && <div className="flex flex-wrap gap-2">
          <Button variant="secondary" onPress={() => navigate(marketplacePaths.connector(connector.connectorId))}><Store size={16} aria-hidden="true" />Open in Marketplace</Button>
          {connector.state === "private" && <Button variant="primary" onPress={() => setConfirm("publish")}>Publish…</Button>}
        </div>}
      </header>

      {connector?.state === "withdrawn" && <Alert status="warning"><Alert.Indicator /><Alert.Content><Alert.Title>You withdrew it</Alert.Title>
        <Alert.Description>No new Work can select it, yours included. People who installed it keep it until they uninstall.</Alert.Description></Alert.Content></Alert>}

      {showForm && <div className="grid gap-8">
        <Card><Card.Content className="grid gap-6 py-2">
          {!connector && <>
            <NameField draft={draft} set={set} problems={problems} touched={touched || Boolean(draft.name)} />
            <TransportField draft={draft} set={set} />
            <ProviderField draft={draft} set={set} problems={problems} providers={providers} />
          </>}
          <AddressField draft={draft} set={set} problems={problems} touched={touched || Boolean(draft.url)} />
          <Separator />
          <AboutFields draft={draft} set={set} problems={problems} touched={touched} categories={categories} />
        </Card.Content></Card>
        {connector && <section aria-label="Danger zone" className="grid divide-y divide-separator border-t border-separator">
          {connector.state === "private" && <DangerRow title={`Delete ${connector.name}`} description="Possible while only you can see it. Your installation goes with it." action="Delete…" onPress={() => setConfirm("delete")} />}
          {connector.state === "published" && <DangerRow title={`Withdraw ${connector.name}`} description="It leaves the Marketplace for good. People who installed it keep it." action="Withdraw…" onPress={() => setConfirm("withdraw")} />}
        </section>}
        {connector ? <SaveBar shown={dirty} note="You have unsaved changes." label="Save changes" isPending={authoring.change.isPending} onDiscard={() => setDraft(draftOf(connector))} onSave={save} />
          : <SaveBar shown note="It starts private to you." label="Add connector" discard="Cancel" isPending={authoring.add.isPending} onDiscard={back} onSave={save} />}
      </div>}
    </div>

    {connector && <Modal><Modal.Backdrop isOpen={confirm === "publish"} onOpenChange={open => { if (!open) setConfirm(null); }}><Modal.Container placement="center" size="md"><Modal.Dialog>
      <Modal.Header><Modal.Heading>Publish {connector.name} to the Marketplace?</Modal.Heading></Modal.Header>
      <Modal.Body className="grid gap-3">
        <Typography.Paragraph>Everyone in this workspace can find and install it.</Typography.Paragraph>
        <Typography.Paragraph size="sm" className="text-muted">Each person signs in with their own account, never yours. Your later changes reach Works started afterwards. Once published you can withdraw it, but not delete it.</Typography.Paragraph>
      </Modal.Body>
      <Modal.Footer><Button variant="tertiary" onPress={() => setConfirm(null)}>Cancel</Button>
        <Button variant="primary" isPending={authoring.publish.isPending} onPress={() => run(authoring.publish, "published", [`Published ${connector.name}`, "It’s in the Marketplace for everyone."], () => setConfirm(null))}>Publish</Button></Modal.Footer>
    </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>}
    {connector && <ConfirmDialog isOpen={confirm === "withdraw"} onOpenChange={open => { if (!open) setConfirm(null); }} title={`Withdraw ${connector.name}?`} confirmLabel="Withdraw"
      onConfirm={() => run(authoring.withdraw, "withdrawn", [`Withdrew ${connector.name}`, "New Works can’t select it. Existing installations stay."])}>
      <p>It leaves the Marketplace and no new Work can select it. People who installed it keep it. This can’t be undone: you can’t publish it again.</p>
    </ConfirmDialog>}
    {connector && <ConfirmDialog isOpen={confirm === "delete"} onOpenChange={open => { if (!open) setConfirm(null); }} title={`Delete ${connector.name}?`} confirmLabel="Delete"
      onConfirm={() => run(authoring.remove, "deleted", [`Deleted ${connector.name}`], back)}>
      <p>Your installation goes with it. Works already running keep it. This can’t be undone.</p>
    </ConfirmDialog>}
  </PageLayout>;
}
