import { Alert, Button } from "@heroui/react";
import { CircleOff, CloudOff } from "lucide-react";
import "./conversation-unavailable.css";

/** Shown in place of the conversation when the Work's state cannot be read:
 * the authority says it does not exist, or the first read failed. */
export function WorkSnapshotUnavailable({ notFound, onRetry }: { notFound: boolean; onRetry: () => void }) {
  if (notFound) return <p className="wca-unavailable" role="status"><CircleOff size={20} aria-hidden="true" />This Work no longer exists</p>;
  return <div className="wca-unavailable" role="alert">
    <CloudOff size={20} aria-hidden="true" />
    <span>This Work's state could not be loaded</span>
    <Button size="sm" variant="ghost" onPress={onRetry}>Retry</Button>
  </div>;
}

/** The page keeps the last state it read and says how old it is until a
 * refresh succeeds again. */
export function WorkSnapshotStaleNotice({ lastUpdatedAt, onRetry }: { lastUpdatedAt: Date; onRetry: () => void }) {
  return <Alert status="warning" role="status">
    <Alert.Indicator />
    <Alert.Content><Alert.Description>Showing the state from {lastUpdatedAt.toLocaleTimeString()}. Updates are failing; actions are checked against the current Work before they apply.</Alert.Description></Alert.Content>
    <Button size="sm" variant="ghost" onPress={onRetry}>Refresh</Button>
  </Alert>;
}
