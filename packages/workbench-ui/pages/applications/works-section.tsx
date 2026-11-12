import { useState } from "react";
import { Alert, Button, Chip, Table, Tag, TagGroup } from "@heroui/react";
import { Link, useNavigate } from "react-router-dom";
import { formatAgo } from "../../components/analytics/work-usage/work-status";
import { applicationWorkPath, labelText, listedApplicationWorks, useApplicationWorks, type ApplicationWork, type LabelPair } from "../../lib/application-work-service";
import type { Application } from "../../lib/application-service";
import { useNow } from "../../lib/use-now";
import { workPresentation } from "../../lib/work-state-presentation";

/** The Application's Works, read-only for its owner: selecting a Work Label
 * on a Work narrows the list to the Works carrying it. */
export function WorksSection({ application }: { application: Application }) {
  const [filter, setFilter] = useState<LabelPair[]>([]);
  const works = useApplicationWorks(application.id, filter);
  // One value per key: picking another value of a key replaces it.
  const pick = (pair: LabelPair) => setFilter(current => [...current.filter(([key]) => key !== pair[0]), pair]);
  const count = works.data?.length;
  return <section className="mk-section" aria-labelledby="ap-works">
    <h3 id="ap-works" className="mk-section-title">Works{count !== undefined && <span className="ml-2 text-muted tabular-nums">{count >= listedApplicationWorks ? `${count}+` : count}</span>}</h3>
    <p className="mk-note">What {application.name} started through the API. You read them here; only {application.name} drives them.</p>
    <LabelFilterBar filter={filter} onChange={setFilter} />
    {works.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{application.name}’s works couldn’t be loaded.</Alert.Description></Alert.Content><Button size="sm" variant="secondary" isPending={works.isFetching} onPress={() => void works.refetch()}>Retry</Button></Alert>}
    <WorksTable application={application} works={works.data} isPending={works.isPending} filtered={filter.length > 0} onPick={pick} />
  </section>;
}

function LabelFilterBar({ filter, onChange }: { filter: LabelPair[]; onChange: (filter: LabelPair[]) => void }) {
  if (filter.length === 0) return <p className="mk-note">Select a label on a Work to show only the Works that carry it.</p>;
  return <div className="flex flex-wrap items-center gap-2">
    <TagGroup aria-label="Label filters" size="sm" onRemove={keys => onChange(filter.filter(pair => !keys.has(labelText(pair))))}>
      <TagGroup.List>{filter.map(pair => <Tag key={labelText(pair)} id={labelText(pair)} textValue={labelText(pair)}><code>{labelText(pair)}</code><Tag.RemoveButton /></Tag>)}</TagGroup.List>
    </TagGroup>
    <Button size="sm" variant="ghost" onPress={() => onChange([])}>Clear</Button>
  </div>;
}

function LabelChips({ labels, onPick }: { labels: Record<string, string>; onPick: (pair: LabelPair) => void }) {
  const pairs = Object.entries(labels).sort(([a], [b]) => a.localeCompare(b));
  if (pairs.length === 0) return null;
  return <span className="flex flex-wrap gap-1">{pairs.map(pair => <Button key={pair[0]} size="sm" variant="ghost"
    className="h-6 min-w-0 rounded-md bg-surface-secondary px-1.5 text-[12px]" aria-label={`Show only Works with ${labelText(pair)}`} onPress={() => onPick(pair)}>
    <code>{labelText(pair)}</code>
  </Button>)}</span>;
}

function WorksTable({ application, works, isPending, filtered, onPick }: { application: Application; works: ApplicationWork[] | undefined; isPending: boolean; filtered: boolean; onPick: (pair: LabelPair) => void }) {
  const navigate = useNavigate();
  const now = useNow(30_000);
  const empty = isPending ? <span role="status">Loading {application.name}’s works…</span>
    : works === undefined ? null
    : filtered ? "No Works carry all these labels."
    : `Works ${application.name} starts through the API show up here.`;
  return <Table><Table.ScrollContainer><Table.Content aria-label={`${application.name} works`} className="w-full" onRowAction={key => navigate(applicationWorkPath(application.id, String(key)))}>
    <Table.Header><Table.Column isRowHeader>Work</Table.Column><Table.Column className="w-48">State</Table.Column><Table.Column className="w-28">Updated</Table.Column></Table.Header>
    <Table.Body renderEmptyState={() => <div className="py-8 text-center text-sm text-muted">{empty}</div>}>
      {(works ?? []).map(work => {
        // The Work waits for the Application's answer, never its owner's.
        const state = workPresentation({ ...work, waiting: false });
        return <Table.Row key={work.id} id={work.id}>
          <Table.Cell><span className="grid gap-1 py-1">
            <Link className="hover:underline" to={applicationWorkPath(application.id, work.id)}>{work.title}</Link>
            <LabelChips labels={work.labels} onPick={onPick} />
          </span></Table.Cell>
          <Table.Cell>{state ? <span className="flex items-center gap-1.5 whitespace-nowrap text-sm"><state.icon size={14} aria-hidden="true" />{state.label}{work.waiting && <Chip size="sm" variant="soft" color="warning"><Chip.Label>Waits for {application.name}</Chip.Label></Chip>}</span> : <span className="text-muted">—</span>}</Table.Cell>
          <Table.Cell>{work.updatedAt ? formatAgo(now - work.updatedAt.getTime()) : "—"}</Table.Cell>
        </Table.Row>;
      })}
    </Table.Body>
  </Table.Content></Table.ScrollContainer></Table>;
}
