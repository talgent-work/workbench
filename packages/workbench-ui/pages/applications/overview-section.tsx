import { useState, type ReactNode } from "react";
import { Card, Chip, ListBox, Meter, Select } from "@heroui/react";
import { LoadState } from "../../components/marketplace/marketplace-shared";
import { useAgentWorkDefaults, useApplicationAgent } from "../../lib/application-agent-service";
import { liveSecrets, type Application } from "../../lib/application-service";
import { listedApplicationWorks, useOpenApplicationWorks } from "../../lib/application-work-service";
import { useInstalledConnectors, useInstalledPlugins } from "../../lib/marketplace-catalog";
import { useApplicationUsage, usageWindowDays, type LabelUsage } from "../../lib/application-usage-service";
import { formatCompactTokenCount } from "../../lib/trace-view-model";
import { tokenCategories, tokenTotal, type TokenUsage } from "../../lib/token-usage";
import { useNow } from "../../lib/use-now";

/** An Application at a glance for its owner: its open Works, its tokens over
 * the measurements kept, what its Works load by default, its secret state and
 * its usage by one Work Label key; a removed one shows only its Works and
 * usage. */
export function OverviewSection({ application, onSection }: { application: Application; onSection: (section: "works" | "agent" | "credentials") => void }) {
  // The first key the Works carry is the breakdown until the owner picks one.
  const [picked, setPicked] = useState("");
  const usage = useApplicationUsage(application.id, picked);
  return <>
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" role="group" aria-label={`${application.name} at a glance`}>
      <OpenWorksStat application={application} onPress={() => onSection("works")} />
      <Stat label={`Tokens, ${usageWindowDays} days`} value={usage.data ? formatCompactTokenCount(tokenTotal(usage.data.usage)) : "–"} detail={usage.data && <TokenParts usage={usage.data.usage} />} />
      {/* A removed Application loads nothing and holds no secret. */}
      {!application.removed && <LoadsByDefaultStat application={application} onPress={() => onSection("agent")} />}
      {!application.removed && <SecretsStat application={application} onPress={() => onSection("credentials")} />}
    </div>
    <section className="mk-section" aria-labelledby="ap-usage">
      <h3 id="ap-usage" className="mk-section-title">Usage</h3>
      <LoadState noun="usage" isPending={usage.isPending} isError={usage.isError} hasData={Boolean(usage.data)} isFetching={usage.isFetching} onRetry={() => void usage.refetch()} />
      {usage.data && <UsageByLabel application={application} keys={usage.data.labelKeys} selected={usage.data.breakdownKey} onSelect={setPicked} total={tokenTotal(usage.data.usage)} breakdown={usage.data.breakdown} />}
    </section>
  </>;
}

function Stat({ label, value, detail, tone, onPress }: { label: string; value: string; detail?: ReactNode; tone?: { color: "warning" | "danger"; text: string }; onPress?: () => void }) {
  const body = <>
    <span className="text-[13px] text-muted">{label}</span>
    <span className="flex items-center gap-2 text-[22px] font-semibold tabular-nums">{value}{tone && <Chip size="sm" variant="soft" color={tone.color}><Chip.Label>{tone.text}</Chip.Label></Chip>}</span>
    {detail}
  </>;
  if (!onPress) return <Card className="mk-group grid gap-1 p-4">{body}</Card>;
  return <Card className="mk-group grid cursor-pointer gap-1 p-4" role="button" tabIndex={0} aria-label={`${label}: ${value}`} onClick={onPress} onKeyDown={event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onPress(); } }}>{body}</Card>;
}

/** How many of the Application's Works are still open. */
function OpenWorksStat({ application, onPress }: { application: Application; onPress: () => void }) {
  const open = useOpenApplicationWorks(application.id);
  const value = open.data === undefined ? "–" : open.data >= listedApplicationWorks ? `${open.data}+` : String(open.data);
  return <Stat label="Open Works" value={value} onPress={onPress} />;
}

/** How many installed Plugins and Connectors every Work of the Application
 * loads unless its start request leaves them out; what every Work loads
 * anyway is not the owner's choice and is not counted. */
function LoadsByDefaultStat({ application, onPress }: { application: Application; onPress: () => void }) {
  const owner = { applicationId: application.id };
  const plugins = useInstalledPlugins(owner);
  const connectors = useInstalledConnectors(owner);
  const agent = useApplicationAgent(application.id);
  const defaults = useAgentWorkDefaults(agent.data?.agentId ?? "");
  const required = new Set((defaults.data?.options?.plugins ?? []).filter(plugin => plugin.required).map(plugin => plugin.name));
  const known = plugins.data && connectors.data && defaults.data;
  const loaded = known ? [...plugins.data.values()].filter(item => item.enabled && !required.has(item.slug)).length
    + connectors.data.filter(item => item.status === "enabled" && !item.required).length : undefined;
  const value = loaded === undefined ? "–" : `${loaded} ${loaded === 1 ? "item" : "items"}`;
  return <Stat label="Loads by default" value={value} onPress={onPress} />;
}

/** Whether the Application can sign in, and whether a rotation is under way. */
function SecretsStat({ application, onPress }: { application: Application; onPress: () => void }) {
  const now = useNow(30_000);
  const secrets = liveSecrets(application, now);
  const overlapping = secrets.some(secret => secret.state === "overlapping");
  const value = secrets.length === 0 ? "None" : overlapping ? "Rotating" : "1 current";
  const tone = secrets.length === 0 ? { color: "danger" as const, text: "Can’t sign in" } : overlapping ? { color: "warning" as const, text: "Overlap" } : undefined;
  return <Stat label="Secrets" value={value} tone={tone} onPress={onPress} />;
}

/** The four token categories, in their order, that a total sums. */
function TokenParts({ usage }: { usage: TokenUsage }) {
  return <span className="text-[12px] text-muted tabular-nums">{tokenCategories.map(category => `${category.label} ${formatCompactTokenCount(usage[category.key])}`).join(" · ")}</span>;
}

function UsageByLabel({ application, keys, selected, onSelect, total, breakdown }: { application: Application; keys: string[]; selected: string; onSelect: (key: string) => void; total: number; breakdown: LabelUsage[] }) {
  if (total === 0) return <p className="mk-note">Usage appears once {application.name} runs Works.</p>;
  if (keys.length === 0) return <p className="mk-note">{application.name}’s Works carry no Work Labels to break their usage down by.</p>;
  return <Card className="mk-group">
    <Card.Content className="grid gap-4 p-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-[14px]"><strong className="text-[20px] font-semibold tabular-nums">{formatCompactTokenCount(total)}</strong> tokens, last {usageWindowDays} days</span>
        <Select aria-label="Break usage down by" className="w-48" value={selected || null} onChange={value => { if (value) onSelect(String(value)); }}>
          <Select.Trigger><Select.Value>{() => selected ? `By ${selected}` : "Break down by"}</Select.Value><Select.Indicator /></Select.Trigger>
          <Select.Popover><ListBox>{keys.map(item => <ListBox.Item key={item} id={item} textValue={item}>{item}<ListBox.ItemIndicator /></ListBox.Item>)}</ListBox></Select.Popover>
        </Select>
      </div>
      {selected && <ul className="grid gap-3" aria-label={`Usage by ${selected}`}>
        {breakdown.map(group => {
          const tokens = tokenTotal(group.usage);
          const name = group.labelled ? `${selected}=${group.value}` : `No ${selected}`;
          return <li key={group.labelled ? `=${group.value}` : "none"}>
            <Meter aria-label={name} value={total ? (tokens / total) * 100 : 0} size="sm">
              <div className="flex w-full flex-wrap justify-between gap-x-3 text-[13px]">
                <span className="min-w-0 break-all">{group.labelled ? <code>{name}</code> : <span className="text-muted">{name}</span>}</span>
                <span className="tabular-nums">{formatCompactTokenCount(tokens)} <span className="text-muted">· {group.workCount} {group.workCount === 1 ? "Work" : "Works"}</span></span>
              </div>
              <Meter.Track><Meter.Fill /></Meter.Track>
            </Meter>
            <TokenParts usage={group.usage} />
          </li>;
        })}
      </ul>}
    </Card.Content>
  </Card>;
}
