import { Toast } from "@heroui/react";
import { notificationQueue } from "../../lib/notifications";
import "./toast-region.css";

// Top-right, below the topbar, overlaying content so notices never shift layout.
export function ToastRegion() {
  return <Toast.Provider queue={notificationQueue} placement="top end" className="app-toasts" width={380}>{({ toast }) =>
    <Toast toast={toast} variant={toast.content.kind}>
      <Toast.Indicator />
      <Toast.Content><Toast.Title>{toast.content.title}</Toast.Title>{toast.content.description && <Toast.Description>{toast.content.description}</Toast.Description>}</Toast.Content>
      {toast.content.action && <Toast.ActionButton onPress={() => { toast.content.action!.onPress(); notificationQueue.close(toast.key); }}>{toast.content.action.label}</Toast.ActionButton>}
      <Toast.CloseButton aria-label="Dismiss notification" />
    </Toast>}
  </Toast.Provider>;
}
