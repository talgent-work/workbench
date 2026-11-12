// PROTOTYPE (Applications) variant C, "Works first": the page is the Application's
// own workbench. Its Works, filtered by Work Labels, fill the page; its
// Agent, what the Agent loads and how it signs in sit in a context rail as
// on a Work page.
import { useMemo, useState } from "react";
import { Button, Card, Modal, Tag, TagGroup } from "@heroui/react";
import { Pencil, Trash2 } from "lucide-react";
import { AgentIcon } from "../../../components/agent/agent-icon";
import { RemoveApplicationDialog } from "./proto-dialogs";
import { labelKey, type LabelFilter, type ProtoApplication } from "./proto-data";
import { WorkDefaults } from "./proto-defaults";
import { Loadout } from "./proto-loadout";
import { AgentEditor, Credentials, Section, UsageByLabel, WorksTable } from "./proto-parts";
import { ApplicationScope } from "./proto-scope";

export function VariantC() {
  return <ApplicationScope>{(application, leave) => <Console key={application.id} application={application} onRemoved={leave} />}</ApplicationScope>;
}

function Console({ application, onRemoved }: { application: ProtoApplication; onRemoved: () => void }) {
  const [filter, setFilter] = useState<LabelFilter>([]);
  const [editing, setEditing] = useState(false);
  const [removing, setRemoving] = useState(false);
  // Every key=value the Application used, as facets; one value per key at a time.
  const facets = useMemo(() => {
    const byKey = new Map<string, Set<string>>();
    for (const work of application.works) for (const [key, value] of Object.entries(work.labels)) byKey.set(key, (byKey.get(key) ?? new Set()).add(value));
    return [...byKey].map(([key, values]) => ({ key, values: [...values].sort() }));
  }, [application.works]);
  const choose = (key: string, keys: Set<string>) => {
    const current = filter.find(([k]) => k === key);
    const picked = [...keys].find(item => !current || item !== labelKey(current));
    const rest = filter.filter(([k]) => k !== key);
    setFilter(picked ? [...rest, [key, picked.slice(key.length + 1)]] : rest);
  };
  const pick = (pair: [string, string]) => setFilter(current => [...current.filter(([key]) => key !== pair[0]), pair]);
  // The Work page's context rail: 280px wide, 40px from the content.
  return <div className="grid gap-10 pb-24 pt-4 lg:grid-cols-[minmax(0,1fr)_280px]">
    <div className="grid min-w-0 content-start gap-8">
      <Section id="c-works" title="Works" count={application.works.length}
        action={filter.length > 0 && <Button size="sm" variant="ghost" onPress={() => setFilter([])}>Clear filters</Button>}>
        {facets.length > 0 && <div className="grid gap-2">
          {facets.map(({ key, values }) => <div key={key} className="flex flex-wrap items-center gap-2">
            <span className="w-20 shrink-0 text-[13px] text-muted"><code>{key}</code></span>
            <TagGroup aria-label={`Filter by ${key}`} size="sm" selectionMode="multiple" selectedKeys={new Set(filter.filter(([k]) => k === key).map(labelKey))}
              onSelectionChange={keys => choose(key, new Set([...(keys === "all" ? [] : keys)].map(String)))}>
              <TagGroup.List>{values.map(value => <Tag key={value} id={`${key}=${value}`}>{value}</Tag>)}</TagGroup.List>
            </TagGroup>
          </div>)}
        </div>}
        <WorksTable application={application} filter={filter} onPick={pick} />
      </Section>
      <Section id="c-usage" title={filter.length ? "Usage of these Works" : "Usage"}><UsageByLabel application={application} filter={filter} /></Section>
    </div>
    <aside className="grid content-start gap-8" aria-label={`${application.name} details`}>
      <Card className="mk-group">
        <Card.Content className="grid gap-3 p-4">
          <div className="flex items-center gap-3">
            <AgentIcon icon={application.agent.icon} size={40} />
            <div className="grid min-w-0 flex-1"><strong className="truncate">{application.agent.name}</strong><span className="text-[13px] text-muted">Runs every Work</span></div>
            <Button size="sm" variant="ghost" isIconOnly aria-label="Edit Agent" onPress={() => setEditing(true)}><Pencil size={15} aria-hidden="true" /></Button>
          </div>
          {application.agent.soul && <p className="line-clamp-3 text-[13px] text-muted">{application.agent.soul}</p>}
        </Card.Content>
      </Card>
      <WorkDefaults application={application} compact />
      <Loadout application={application} compact />
      <Section id="c-credentials" title="Credentials"><Credentials application={application} compact /></Section>
      <Button size="sm" variant="ghost" className="justify-self-start text-danger" onPress={() => setRemoving(true)}><Trash2 size={15} aria-hidden="true" />Remove application</Button>
    </aside>
    {editing && <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open) setEditing(false); }}><Modal.Container placement="center" size="lg"><Modal.Dialog>
      <Modal.Header><Modal.Heading>{application.name}’s Agent</Modal.Heading></Modal.Header>
      <Modal.Body><AgentEditor application={application} /></Modal.Body>
    </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>}
    {removing && <RemoveApplicationDialog application={application} onClose={() => setRemoving(false)} onRemoved={onRemoved} />}
  </div>;
}
