import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Tabs } from "@heroui/react";
import { FileText, FolderKanban } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import { TokenReport, type TokenRanking } from "../../components/analytics/token-report";
import { PageLayout } from "../../components/shell/page-layout";
import { dashboardReportPeriod } from "../../lib/dashboard-report";
import { usePersonalTokenAnalytics } from "../../lib/personal-usage-service";
import { untitledWorkLabel, useStandaloneWorks } from "../../lib/standalone-work-service";
import { dashboardWindows, parseDashboardWindow } from "../../lib/dashboard";
import type { TokenAnalytics } from "../../lib/token-analytics";
import { tokenTotals } from "../../lib/trace-view-model";

const topWorkCount = 10;

/** Usage: the person's usage across all their Works, laid out as Insights'
 * Spend, its one ranking being Top works. */
export function UsagePage() {
  const [search, setSearch] = useSearchParams();
  const range = parseDashboardWindow(search.get("window"));
  // The window moves with the clock, as Insights' does.
  const [anchor, setAnchor] = useState(() => new Date());
  useEffect(() => { const timer = window.setInterval(() => setAnchor(new Date()), 30_000); return () => window.clearInterval(timer); }, []);
  const period = useMemo(() => dashboardReportPeriod(range, anchor), [range, anchor]);
  const usage = usePersonalTokenAnalytics(range, period);
  const [hover, setHover] = useState<{ index: number; chart: string } | null>(null);
  const windowTabs = <Tabs selectedKey={range} onSelectionChange={key => { setHover(null); setSearch({ window: String(key) }, { replace: true }); }}>
    <Tabs.ListContainer><Tabs.List aria-label="Time window">{dashboardWindows.map(item => <Tabs.Tab key={item} id={item} aria-controls="usage-report">{item.toUpperCase()}<Tabs.Indicator /></Tabs.Tab>)}</Tabs.List></Tabs.ListContainer>
  </Tabs>;
  return <PageLayout workspaceTitle="Usage" topActions={windowTabs}>
    <div id="usage-report" className="flex flex-col gap-4 pb-24">
      {usage.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Your usage couldn’t be loaded.</Alert.Description></Alert.Content><Button size="sm" variant="secondary" isPending={usage.isFetching} onPress={() => void usage.refetch()}>Retry</Button></Alert>}
      {usage.data ? <UsageReport analytics={usage.data} hover={hover} onHover={setHover} /> : !usage.isError && <span className="sr-only" role="status">Loading Usage</span>}
    </div>
  </PageLayout>;
}

function UsageReport({ analytics, hover, onHover }: { analytics: TokenAnalytics; hover: { index: number; chart: string } | null; onHover: (hover: { index: number; chart: string } | null) => void }) {
  const personal = useStandaloneWorks();
  const rankings: TokenRanking[] = [{ title: "Top works", rows: topWorks(analytics, new Map((personal.data ?? []).map(work => [work.id, work.title]))) }];
  return <TokenReport analytics={analytics} rankings={rankings} hover={hover} onHover={onHover} />;
}

// A person's usage splits by Work; a Work of a Project shows as one.
function topWorks(analytics: TokenAnalytics, titles: Map<string, string>): TokenRanking["rows"] {
  const projectWorks = new Set(analytics.metrics.buckets.filter(bucket => bucket.projectId).map(bucket => bucket.agentWorkId ?? ""));
  return [...tokenTotals(analytics.metrics.buckets, "work")].sort((left, right) => right[1] - left[1]).slice(0, topWorkCount).map(([workId, totalTokens]) => {
    const project = projectWorks.has(workId) && !titles.has(workId);
    return { key: workId, totalTokens, label: titles.get(workId) ?? untitledWorkLabel, detail: workId,
      icon: project ? <FolderKanban size={16} aria-hidden="true" /> : <FileText size={16} aria-hidden="true" /> };
  });
}
