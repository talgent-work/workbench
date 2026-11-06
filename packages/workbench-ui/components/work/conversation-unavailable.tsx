import { Lock, ShieldOff } from "lucide-react";
import "./conversation-unavailable.css";

/** The single line shown in place of a conversation the caller cannot read. */
export function ConversationUnavailable({ withdrawn = false }: { withdrawn?: boolean }) {
  const Icon = withdrawn ? ShieldOff : Lock;
  return <p className="wca-unavailable" role="status">
    <Icon size={20} aria-hidden="true" />
    {withdrawn ? "Your access to this conversation was withdrawn" : "This conversation is private"}
  </p>;
}
