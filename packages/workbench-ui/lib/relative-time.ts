/** How long ago a moment was, in the workspace's compact form ("5m ago"). */
export function timeAgo(date: Date | null | undefined, now = Date.now()): string {
  if (!date) return "just now";
  const diffSeconds = Math.max(0, Math.floor((now - date.getTime()) / 1000));
  if (diffSeconds < 60) return "just now";
  const minutes = Math.floor(diffSeconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

/** How long until a moment, in the same compact form ("in 23h"). */
export function timeUntil(date: Date, now = Date.now()): string {
  const minutes = Math.ceil((date.getTime() - now) / 60_000);
  if (minutes <= 0) return "now";
  if (minutes < 60) return `in ${minutes}m`;
  // Hours up to two days, so a one-day overlap reads "in 23h", not "in 0d".
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `in ${hours}h`;
  return `in ${Math.floor(hours / 24)}d`;
}
