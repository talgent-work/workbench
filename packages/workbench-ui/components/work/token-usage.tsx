import { Button, Tooltip } from "@heroui/react";
import { ChartNoAxesColumnIncreasing } from "lucide-react";
import { formatCompactTokenCount } from "../../lib/trace-view-model";
import { tokenCategories, tokenTotal, tokenUsageFromMeasurements } from "../../lib/token-usage";
import { TokenBreakdown } from "../analytics/token-breakdown";
import { RailSection } from "../rail/rail-section";

type Totals = Array<{ metricKind: string; value: number }>;

/** The Work context card's Token Usage section, whose action opens the Work's
 * Token Usage reader. */
export function WorkTokenSection({ totals, onOpenTimeline }: { totals: Totals; onOpenTimeline: () => void }) {
  return <RailSection title="Token Usage" className="cr-token-section" action={<Tooltip><Button isIconOnly size="sm" variant="ghost" aria-label="View Work timeline" onPress={onOpenTimeline}><ChartNoAxesColumnIncreasing size={15} aria-hidden="true" /></Button><Tooltip.Content>View Work timeline</Tooltip.Content></Tooltip>}>
    <TokenUsage totals={totals} />
  </RailSection>;
}

/** A Work's token usage: the total, then on hover or focus a bar of its four
 * parts, their amounts and the Cache hit rate. */
function TokenUsage({ totals: measurements }: { totals: Totals }) {
  const usage = tokenUsageFromMeasurements(measurements);
  const total = tokenTotal(usage);
  return <div className="cr-token-usage">
    <strong className="cr-token-total">{formatCompactTokenCount(total)}</strong>
    {total > 0 && <div className="cr-token-bar" aria-hidden="true">{tokenCategories.map(({ key }) => <span key={key} data-token-tone={key} style={{ flexGrow: usage[key] / total }} />)}</div>}
    <TokenBreakdown usage={usage} className="cr-token-breakdown" />
  </div>;
}
