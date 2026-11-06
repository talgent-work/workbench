import { formatCompactTokenCount } from "../../lib/trace-view-model";
import { cacheHitRate, formatHitRate, tokenCategories, type TokenUsage } from "../../lib/token-usage";
import "./token-breakdown.css";

/** One usage's Cache hit rate and its four categories, as one list. Shared by
 * the Work context card and the usage charts' hover cards. */
export function TokenBreakdown({ usage, className }: { usage: TokenUsage; className?: string }) {
  const rate = cacheHitRate(usage);
  return <dl className={["token-breakdown", className].filter(Boolean).join(" ")}>
    {rate !== null && <div><dt><HitRateMark rate={rate} />Cache hit rate</dt><dd>{formatHitRate(rate)}</dd></div>}
    {tokenCategories.map(({ key, label }) => <div key={key}><dt><i data-token-tone={key} />{label}</dt><dd>{formatCompactTokenCount(usage[key])}</dd></div>)}
  </dl>;
}

/** Cache hit rate's legend mark: a small ring filled to the rate. It leads the
 * list in the darkest tone, above the categories' lightening swatches. */
function HitRateMark({ rate }: { rate: number }) {
  const circumference = 2 * Math.PI * 3;
  return <svg className="token-hit-mark" width="8" height="8" viewBox="0 0 8 8" aria-hidden="true">
    <circle cx="4" cy="4" r="3" />
    <circle cx="4" cy="4" r="3" data-filled="" strokeDasharray={`${circumference * Math.min(Math.max(rate, 0), 1)} ${circumference}`} transform="rotate(-90 4 4)" />
  </svg>;
}
