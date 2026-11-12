// PROTOTYPE (Applications) variant B, "Tabs": the topbar names the Application and
// switches its sections; each section fills the page on its own.
import { useState } from "react";
import { Button, Card, Chip, Tabs } from "@heroui/react";
import { Trash2 } from "lucide-react";
import { RemoveApplicationDialog } from "./proto-dialogs";
import { formatTokens, type LabelFilter, type ProtoApplication } from "./proto-data";
import { WorkDefaults } from "./proto-defaults";
import { Loadout } from "./proto-loadout";
import { AgentEditor, Credentials, LabelFilterBar, Section, UsageByLabel, WorksTable } from "./proto-parts";
import { ApplicationScope } from "./proto-scope";

const sections = [["overview", "Overview"], ["works", "Works"], ["agent", "Agent"], ["credentials", "Credentials"], ["settings", "Settings"]] as const;
type SectionId = typeof sections[number][0];

export function VariantB() {
  const [tab, setTab] = useState<SectionId>("overview");
  const tabs = <Tabs className="p-perspective-tabs" selectedKey={tab} onSelectionChange={key => setTab(key as SectionId)}>
    <Tabs.ListContainer><Tabs.List aria-label="Application section">{sections.map(([id, label]) => <Tabs.Tab key={id} id={id}>{label}<Tabs.Indicator /></Tabs.Tab>)}</Tabs.List></Tabs.ListContainer>
  </Tabs>;
  return <ApplicationScope section={sections.find(([id]) => id === tab)![1]} center={tabs}>
    {(application, leave) => <div className="mx-auto flex w-full max-w-[960px] flex-col gap-8 pb-24 pt-4">
      <Body key={application.id} application={application} tab={tab} onTab={setTab} onRemoved={() => { leave(); setTab("overview"); }} />
    </div>}
  </ApplicationScope>;
}

function Body({ application, tab, onTab, onRemoved }: { application: ProtoApplication; tab: SectionId; onTab: (tab: SectionId) => void; onRemoved: () => void }) {
  const [filter, setFilter] = useState<LabelFilter>([]);
  const [removing, setRemoving] = useState(false);
  const pick = (pair: [string, string]) => setFilter(current => [...current.filter(([key]) => key !== pair[0]), pair]);
  const open = application.works.filter(work => work.state !== "closed").length;
  const tokens = application.works.reduce((sum, work) => sum + work.tokens, 0);
  const overlapping = application.secrets.some(secret => secret.state === "overlapping");
  const loaded = application.plugins.filter(item => item.enabled).length + application.connectors.filter(item => item.enabled).length;
  return <>
    {tab === "overview" && <>
      <div className="grid gap-3 sm:grid-cols-4">
        <Stat label="Open Works" value={String(open)} onPress={() => onTab("works")} />
        <Stat label="Tokens, 30 days" value={formatTokens(tokens)} onPress={() => onTab("works")} />
        <Stat label="Loads by default" value={`${loaded} ${loaded === 1 ? "item" : "items"}`} onPress={() => onTab("agent")} />
        <Stat label="Secrets" value={overlapping ? "Rotating" : application.secrets.length ? "1 current" : "None"} tone={overlapping ? "warning" : application.secrets.length ? undefined : "danger"} onPress={() => onTab("credentials")} />
      </div>
      <Section id="b-usage" title="Usage"><UsageByLabel application={application} /></Section>
    </>}
    {tab === "works" && <Section id="b-works" title="Works" count={application.works.length}>
      <LabelFilterBar filter={filter} onChange={setFilter} />
      <WorksTable application={application} filter={filter} onPick={pick} />
    </Section>}
    {tab === "agent" && <>
      <Section id="b-agent" title="Appearance and Soul"><AgentEditor application={application} /></Section>
      <WorkDefaults application={application} />
      <Loadout application={application} />
    </>}
    {tab === "credentials" && <Section id="b-credentials" title="Credentials">
      <p className="text-[14px] text-muted">Your program exchanges the client ID and a secret for a short-lived token with the OAuth 2.0 client credentials grant.</p>
      <Credentials application={application} />
    </Section>}
    {tab === "settings" && <Section id="b-remove" title="Remove application">
      <p className="text-[14px] text-muted">Revokes every secret and closes its open Works. You can’t restore it.</p>
      <div><Button size="sm" variant="danger-soft" onPress={() => setRemoving(true)}><Trash2 size={15} aria-hidden="true" />Remove application</Button></div>
    </Section>}
    {removing && <RemoveApplicationDialog application={application} onClose={() => setRemoving(false)} onRemoved={onRemoved} />}
  </>;
}

function Stat({ label, value, tone, onPress }: { label: string; value: string; tone?: "warning" | "danger"; onPress: () => void }) {
  return <Card className="mk-group cursor-pointer p-4" role="button" tabIndex={0} onClick={onPress} onKeyDown={event => { if (event.key === "Enter") onPress(); }}>
    <span className="text-[13px] text-muted">{label}</span>
    <span className="flex items-center gap-2 text-[22px] font-semibold">{value}{tone && <Chip size="sm" variant="soft" color={tone}><Chip.Label>{tone === "warning" ? "Overlap" : "Can’t sign in"}</Chip.Label></Chip>}</span>
  </Card>;
}
