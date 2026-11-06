import { ToastQueue } from "@heroui/react";

// One global notification entry. Non-blocking results and background
// refresh failures are Toasts; problems handled in the current flow use a
// regional Alert instead. A key identifies the operation, so a new notice for
// the same operation replaces the previous one instead of stacking.
export type NotificationKind = "default" | "success" | "warning" | "danger";
export interface Notification {
  title: string;
  description?: string;
  kind: NotificationKind;
  action?: { label: string; onPress: () => void };
}

export const notificationQueue = new ToastQueue<Notification>({ maxVisibleToasts: 3 });
const open = new Map<string, string>();

export function notify(key: string, kind: NotificationKind, title: string, description?: string, options: { timeout?: number; action?: Notification["action"] } = {}) {
  // Danger notices stay until dismissed; HeroUI has no in-place update, so the
  // notice for the same operation is replaced.
  const timeout = options.timeout ?? (kind === "danger" ? 0 : 5000);
  const existing = open.get(key);
  if (existing) notificationQueue.close(existing);
  const id = notificationQueue.add({ title, description, kind, action: options.action }, {
    timeout: timeout || undefined,
    onClose: () => { if (open.get(key) === id) open.delete(key); },
  });
  open.set(key, id);
}

// Recovery clears the failure notice of the same operation.
export function dismiss(key: string) {
  const id = open.get(key);
  if (!id) return;
  notificationQueue.close(id);
  open.delete(key);
}
