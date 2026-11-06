import { Alert, AlertDialog, Button, SearchField, Skeleton } from "@heroui/react";
import { Bot, Plug, Sparkles } from "lucide-react";
import type { ReactNode } from "react";

// One hue per native component type, used on every Marketplace page.
const capabilityIcons = { skill: Sparkles, agent: Bot, mcp: Plug } as const;
export function CapIcon({ kind, size = 16 }: { kind: keyof typeof capabilityIcons; size?: number }) {
  const Icon = capabilityIcons[kind];
  return <Icon size={size} className={`mk-cap mk-cap-${kind}`} aria-hidden="true" />;
}

export function SearchBar({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  return <div className="mk-toolbar"><SearchField aria-label={label} value={value} onChange={onChange} className="mk-search">
    <SearchField.Group><SearchField.SearchIcon /><SearchField.Input placeholder={label} /><SearchField.ClearButton aria-label="Clear search" /></SearchField.Group>
  </SearchField></div>;
}

export function NothingMatches({ onClear }: { onClear: () => void }) {
  return <div className="mk-empty"><p><strong>Nothing matches</strong></p><Button size="sm" variant="secondary" onPress={onClear}>Clear search</Button></div>;
}

// Loading text stays screen-reader-only; a failed refresh keeps the last good
// list with a warning, and a list that never loaded offers retry in its place.
export function LoadState({ noun, isPending, isError, hasData, isFetching, onRetry }: { noun: string; isPending: boolean; isError: boolean; hasData: boolean; isFetching: boolean; onRetry: () => void }) {
  if (isPending) return <div className="mk-skeletons" aria-busy="true"><p className="sr-only" role="status">Loading {noun}…</p>{[0, 1, 2].map(index => <Skeleton key={index} className="mk-skeleton" />)}</div>;
  if (!isError) return null;
  return <Alert status={hasData ? "warning" : "danger"}><Alert.Indicator /><Alert.Content>
    <Alert.Title>{hasData ? `${capitalize(noun)} couldn’t be refreshed` : `${capitalize(noun)} couldn’t be loaded`}</Alert.Title>
    {hasData && <Alert.Description>Showing what was loaded before.</Alert.Description>}
    <Button size="sm" variant="secondary" className="mt-2" isPending={isFetching} onPress={onRetry}>Try again</Button>
  </Alert.Content></Alert>;
}
const capitalize = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

// Destructive confirmation; Cancel receives focus first.
export function ConfirmDialog({ isOpen, onOpenChange, title, children, confirmLabel, onConfirm }: { isOpen: boolean; onOpenChange: (open: boolean) => void; title: string; children: ReactNode; confirmLabel: string; onConfirm: () => void }) {
  return <AlertDialog><AlertDialog.Backdrop isOpen={isOpen} onOpenChange={onOpenChange}><AlertDialog.Container><AlertDialog.Dialog className="sm:max-w-[420px]">
    <AlertDialog.Header><AlertDialog.Icon status="danger" /><AlertDialog.Heading>{title}</AlertDialog.Heading></AlertDialog.Header>
    <AlertDialog.Body>{children}</AlertDialog.Body>
    <AlertDialog.Footer><Button slot="close" variant="tertiary" autoFocus>Cancel</Button><Button slot="close" variant="danger" onPress={onConfirm}>{confirmLabel}</Button></AlertDialog.Footer>
  </AlertDialog.Dialog></AlertDialog.Container></AlertDialog.Backdrop></AlertDialog>;
}

