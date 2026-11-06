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
