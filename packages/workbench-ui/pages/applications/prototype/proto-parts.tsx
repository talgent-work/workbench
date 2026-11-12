// PROTOTYPE (Applications): the Application blocks every variant arranges its own way.
import { useMemo, useState, type ReactNode } from "react";
import { Button, Card, Chip, Input, Label, ListBox, Meter, Select, Table, Tag, TagGroup, TextArea, TextField, ToggleButton, ToggleButtonGroup, Tooltip, type Selection } from "@heroui/react";
import { Check, CircleDot, CirclePause, CircleStop, KeyRound, Loader, RotateCw, Trash2 } from "lucide-react";
import { AgentIcon } from "../../../components/agent/agent-icon";
import { agentColors, agentGlyphs, type AgentColor } from "../../../lib/agent-icon";
import { useNow } from "../../../lib/use-now";
import { CopyValue, RevokeSecretDialog, RotateSecretDialog } from "./proto-dialogs";
import { formatAgo, formatIn, formatTokens, overlapHours, useProtoStore, type ClientSecret, type LabelFilter, labelKey, matches, type ProtoApplication, type WorkState } from "./proto-data";

export function Section({ id, title, count, action, children }: { id: string; title: string; count?: number; action?: ReactNode; children: ReactNode }) {
  return <section className="mk-section" aria-labelledby={id}>
    <div className="mk-section-bar"><h3 id={id} className="mk-section-title">{title}{count !== undefined && <span className="mk-count">{count}</span>}</h3>{action}</div>
    {children}
  </section>;
}

/** The client ID and the secret lifecycle: one current secret, at most one
 * overlapping secret on its way out. */
export function Credentials({ application, compact = false }: { application: ProtoApplication; compact?: boolean }) {
  const [rotating, setRotating] = useState(false);
  const [revoking, setRevoking] = useState<ClientSecret | null>(null);
  const ordered = [...application.secrets].sort((a, b) => (a.state === "current" ? -1 : 1) - (b.state === "current" ? -1 : 1));
  return <>
    <Card className="mk-group">
      <ul className="mk-group-list" aria-label={`${application.name} credentials`}>
        <li className="!items-center">
          <KeyRound size={18} className="shrink-0 text-muted" aria-hidden="true" />
          <span className="mk-row-text min-w-0 flex-1"><strong>Client ID</strong><span><code>{application.clientId}</code></span></span>
          <CopyValue value={application.clientId} label="client ID" />
        </li>
        {ordered.length === 0 && <li><span className="mk-row-text flex-1"><strong className="text-danger">No secret</strong><span>{application.name} can’t sign in. Rotate to issue one.</span></span></li>}
        {ordered.map(secret => <SecretRow key={secret.id} secret={secret} compact={compact} onRevoke={() => setRevoking(secret)} />)}
      </ul>
    </Card>
    <div className="mk-actions">
      <Button size="sm" variant="secondary" onPress={() => setRotating(true)}><RotateCw size={15} aria-hidden="true" />Rotate secret</Button>
      {!compact && <p className="mk-note">Rotating keeps the old secret working for {overlapHours} hours.</p>}
    </div>
    {rotating && <RotateSecretDialog application={application} onClose={() => setRotating(false)} />}
    {revoking && <RevokeSecretDialog application={application} secret={revoking} onClose={() => setRevoking(null)} />}
  </>;
}

function SecretRow({ secret, compact, onRevoke }: { secret: ClientSecret; compact: boolean; onRevoke: () => void }) {
  const now = useNow(30_000);
  const overlapping = secret.state === "overlapping" && secret.endsAt !== undefined;
  const left = overlapping ? Math.max(0, Math.min(100, ((secret.endsAt! - now) / (overlapHours * 3_600_000)) * 100)) : 100;
  return <li className="!items-center">
    <span className={`size-2.5 shrink-0 rounded-full ${overlapping ? "bg-warning" : "bg-success"}`} aria-hidden="true" />
    <span className="mk-row-text min-w-0 flex-1">
      <strong><code>wbs_{secret.hint}</code><Chip size="sm" variant="soft" color={overlapping ? "warning" : "success"}><Chip.Label>{overlapping ? "Overlapping" : "Current"}</Chip.Label></Chip></strong>
      <span>Issued {formatAgo(now - secret.issuedAt)}{overlapping && <> · ends {formatIn(secret.endsAt! - now)}</>}</span>
      {overlapping && !compact && <Meter aria-label="Overlap left" value={left} size="sm" color="warning" className="mt-1 max-w-64"><Meter.Track><Meter.Fill /></Meter.Track></Meter>}
    </span>
    <Tooltip delay={300}><Button size="sm" variant="ghost" isIconOnly aria-label={`Revoke secret ${secret.hint}`} onPress={onRevoke}><Trash2 size={15} aria-hidden="true" /></Button><Tooltip.Content>Revoke now</Tooltip.Content></Tooltip>
  </li>;
}

/** The Application's Agent: name, icon and Soul, as a person's Work Agent. */
export function AgentEditor({ application }: { application: ProtoApplication }) {
  const store = useProtoStore();
  const [draft, setDraft] = useState(application.agent);
  const dirty = JSON.stringify(draft) !== JSON.stringify(application.agent);
  const pick = (part: "glyph" | "color") => (keys: Selection) => {
    const id = [...keys][0];
    if (id !== undefined) setDraft(current => ({ ...current, icon: { ...current.icon, [part]: String(id) } }));
  };
  return <Card className="mk-group">
    <Card.Content className="grid gap-5 p-5">
      <div className="flex items-start gap-4">
        <AgentIcon icon={draft.icon} size={56} />
        <TextField className="min-w-0 flex-1" value={draft.name} onChange={name => setDraft(current => ({ ...current, name }))}><Label>Name</Label><Input /></TextField>
      </div>
      <div className="flex flex-wrap gap-6">
        <ToggleButtonGroup aria-label="Icon" selectionMode="single" disallowEmptySelection isDetached size="sm" className="flex-wrap justify-start" selectedKeys={new Set([draft.icon.glyph])} onSelectionChange={pick("glyph")}>
          {agentGlyphs.map(({ id, label, Icon }) => <ToggleButton key={id} id={id} isIconOnly aria-label={label} variant="ghost"><Icon size={16} aria-hidden="true" /></ToggleButton>)}
        </ToggleButtonGroup>
        <ToggleButtonGroup aria-label="Color" selectionMode="single" disallowEmptySelection isDetached size="sm" selectedKeys={new Set([draft.icon.color])} onSelectionChange={pick("color")}>
          {(Object.keys(agentColors) as AgentColor[]).map(color => <ToggleButton key={color} id={color} isIconOnly aria-label={color} variant="ghost">
            <span aria-hidden="true" className="agent-color-swatch" style={{ background: agentColors[color] }}>{draft.icon.color === color && <Check size={12} strokeWidth={3} />}</span>
          </ToggleButton>)}
        </ToggleButtonGroup>
      </div>
      <TextField value={draft.soul} onChange={soul => setDraft(current => ({ ...current, soul }))}><Label>Soul</Label><TextArea rows={4} placeholder="How this application’s Agent should talk and work" /></TextField>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="mk-muted min-w-[16rem] flex-1 text-[13px]">Every Work of {application.name} runs as this Agent and shares its memory.</p>
        <div className="ml-auto flex gap-2">
          <Button size="sm" variant="ghost" isDisabled={!dirty} onPress={() => setDraft(application.agent)}>Discard</Button>
          <Button size="sm" isDisabled={!dirty || !draft.name.trim()} onPress={() => store.saveAgent(application.id, draft)}>Save</Button>
        </div>
      </div>
    </Card.Content>
  </Card>;
}

const stateView: Record<WorkState, { label: string; Icon: typeof CircleDot; className: string }> = {
  running: { label: "Running", Icon: Loader, className: "text-accent" },
  waiting: { label: "Waiting for input", Icon: CirclePause, className: "text-warning" },
  idle: { label: "Idle", Icon: CircleDot, className: "text-muted" },
  closed: { label: "Closed", Icon: CircleStop, className: "text-muted" },
};


/** Active Work Label filters; each removes on its own. */
export function LabelFilterBar({ filter, onChange }: { filter: LabelFilter; onChange: (filter: LabelFilter) => void }) {
  if (filter.length === 0) return <p className="mk-note">Select a label on a Work to filter by it.</p>;
  return <div className="flex flex-wrap items-center gap-2">
    <TagGroup aria-label="Label filters" size="sm" onRemove={keys => onChange(filter.filter(pair => !keys.has(labelKey(pair))))}>
      <TagGroup.List>{filter.map(pair => <Tag key={labelKey(pair)} id={labelKey(pair)}><code>{labelKey(pair)}</code><Tag.RemoveButton /></Tag>)}</TagGroup.List>
    </TagGroup>
    <Button size="sm" variant="ghost" onPress={() => onChange([])}>Clear</Button>
  </div>;
}

export function LabelChips({ labels, onPick }: { labels: Record<string, string>; onPick?: (pair: [string, string]) => void }) {
  return <span className="flex flex-wrap gap-1">{Object.entries(labels).map(pair => onPick
    ? <Button key={pair[0]} size="sm" variant="ghost" className="h-6 min-w-0 rounded-md bg-surface-secondary px-1.5 text-[12px]" aria-label={`Filter by ${labelKey(pair)}`} onPress={() => onPick(pair)}><code>{labelKey(pair)}</code></Button>
    : <Chip key={pair[0]} size="sm" variant="soft"><Chip.Label><code>{labelKey(pair)}</code></Chip.Label></Chip>)}</span>;
}

/** The Application's Works, read-only for its owner. */
export function WorksTable({ application, filter, onPick }: { application: ProtoApplication; filter: LabelFilter; onPick: (pair: [string, string]) => void }) {
  const works = application.works.filter(work => matches(work, filter));
  const now = useNow(30_000);
  return <Table><Table.ScrollContainer><Table.Content aria-label={`${application.name} works`} className="w-full">
    <Table.Header><Table.Column isRowHeader>Work</Table.Column><Table.Column className="w-40">State</Table.Column><Table.Column className="w-24">Tokens</Table.Column><Table.Column className="w-24">Updated</Table.Column></Table.Header>
    <Table.Body renderEmptyState={() => <div className="py-8 text-center text-sm text-muted">{application.works.length === 0 ? `Works ${application.name} starts through the API show up here.` : "No Works have all these labels."}</div>}>
      {works.map(work => {
        const state = stateView[work.state];
        return <Table.Row key={work.id} id={work.id}>
          <Table.Cell><span className="grid gap-1 py-1"><span className="hover:underline">{work.title}</span><LabelChips labels={work.labels} onPick={onPick} /></span></Table.Cell>
          <Table.Cell><span className={`flex items-center gap-1.5 whitespace-nowrap text-sm ${state.className}`}><state.Icon size={14} aria-hidden="true" />{state.label}</span></Table.Cell>
          <Table.Cell>{formatTokens(work.tokens)}</Table.Cell>
          <Table.Cell>{formatAgo(now - work.updatedAt)}</Table.Cell>
        </Table.Row>;
      })}
    </Table.Body>
  </Table.Content></Table.ScrollContainer></Table>;
}

/** Usage broken down by one Work Label key. */
export function UsageByLabel({ application, filter = [] }: { application: ProtoApplication; filter?: LabelFilter }) {
  const keys = useMemo(() => [...new Set(application.works.flatMap(work => Object.keys(work.labels)))], [application.works]);
  const [key, setKey] = useState(keys[0] ?? "");
  const works = application.works.filter(work => matches(work, filter));
  const total = works.reduce((sum, work) => sum + work.tokens, 0);
  const rows = [...works.reduce((map, work) => map.set(work.labels[key] ?? "(none)", (map.get(work.labels[key] ?? "(none)") ?? 0) + work.tokens), new Map<string, number>())].sort((a, b) => b[1] - a[1]);
  if (keys.length === 0) return <p className="mk-note">Usage appears once {application.name} starts Works.</p>;
  return <Card className="mk-group">
    <Card.Content className="grid gap-4 p-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-[14px]"><strong className="text-[20px] font-semibold">{formatTokens(total)}</strong> tokens, last 30 days</span>
        <Select aria-label="Break down by" className="w-44" value={key} onChange={value => setKey(String(value ?? keys[0]))}>
          <Select.Trigger><Select.Value>{() => `By ${key}`}</Select.Value><Select.Indicator /></Select.Trigger>
          <Select.Popover><ListBox>{keys.map(item => <ListBox.Item key={item} id={item} textValue={item}>{item}<ListBox.ItemIndicator /></ListBox.Item>)}</ListBox></Select.Popover>
        </Select>
      </div>
      <ul className="grid gap-3" aria-label={`Usage by ${key}`}>
        {rows.map(([value, tokens]) => <li key={value}>
          <Meter aria-label={`${key}=${value}`} value={total ? (tokens / total) * 100 : 0} size="sm">
            <div className="flex w-full justify-between text-[13px]"><code>{key}={value}</code><span className="text-muted">{formatTokens(tokens)}</span></div>
            <Meter.Track><Meter.Fill /></Meter.Track>
          </Meter>
        </li>)}
      </ul>
    </Card.Content>
  </Card>;
}
