import { Button, Chip, Typography } from "@heroui/react";
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

const stateLabel: Record<string, string> = { private: "Private", published: "Published", withdrawn: "Withdrawn" };
const stateColor: Record<string, "default" | "success" | "warning"> = { private: "default", published: "success", withdrawn: "warning" };

export function StateChip({ state }: { state: string }) {
  return <Chip size="sm" variant="soft" color={stateColor[state] ?? "default"}><Chip.Label>{stateLabel[state] ?? state}</Chip.Label></Chip>;
}

/** One fixed fact in the item header. */
export function Fact({ icon: Icon, children }: { icon: LucideIcon; children: ReactNode }) {
  return <span className="inline-flex items-center gap-1.5"><Icon size={14} aria-hidden="true" />{children}</span>;
}

/** The sticky bar that unsaved changes raise. */
export function SaveBar({ shown, label, onDiscard, onSave, note, discard = "Discard", isPending }: { shown: boolean; label: string; onDiscard: () => void; onSave: () => void; note: string; discard?: string; isPending?: boolean }) {
  return <div aria-hidden={!shown} className={`sticky bottom-6 z-10 flex flex-wrap items-center justify-between gap-4 rounded-2xl bg-surface px-5 py-3 shadow-lg ring-1 ring-separator transition-all duration-200 motion-reduce:transition-none ${shown ? "translate-y-0 opacity-100" : "pointer-events-none translate-y-3 opacity-0"}`}>
    <Typography.Paragraph size="sm" className="text-muted" role="status">{shown ? note : ""}</Typography.Paragraph>
    <span className="flex gap-2"><Button size="sm" variant="tertiary" onPress={onDiscard} isDisabled={!shown || isPending}>{discard}</Button><Button size="sm" variant="primary" onPress={onSave} isDisabled={!shown} isPending={isPending}>{label}</Button></span>
  </div>;
}

/** The last row: deleting a Private item or withdrawing a Published one. */
export function DangerRow({ title, description, action, onPress }: { title: string; description: string; action: string; onPress: () => void }) {
  return <div className="flex flex-wrap items-center justify-between gap-4 py-4">
    <div className="grid gap-0.5"><Typography.Paragraph className="font-medium">{title}</Typography.Paragraph><Typography.Paragraph size="sm" className="text-muted">{description}</Typography.Paragraph></div>
    <Button size="sm" variant="danger-soft" onPress={onPress}>{action}</Button>
  </div>;
}
