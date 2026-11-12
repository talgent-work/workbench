import { createContext, useContext, type ComponentType, type ReactNode } from "react";

/** What a page shared by both entries asks of its frame. */
export type PageLayoutProps = {
  children: ReactNode;
  workspaceTitle?: string;
  topActions?: ReactNode;
  centerActions?: ReactNode;
  width?: "contained" | "fluid";
  /** Heads the breadcrumb, such as the switcher for the object in view; the
   * title then adds no crumb of its own. */
  lead?: ReactNode;
  /** Levels between the entry and the current page, such as Marketplace › Plugins. */
  trail?: Array<{ label: string; onPress: () => void }>;
};

/** Where an entry serves a person's own pages. */
export type PersonalPaths = { newWork: string; works: string; work: (workId: string) => string; library: string; usage: string };

/** A person's own pages under root; at an entry's root, its New work page is
 * the root itself. */
export function personalPathsAt(root: "" | `/${string}`): PersonalPaths {
  return {
    newWork: root ? `${root}/new` : "/",
    works: `${root}/works`,
    work: workId => `${root}/works/${encodeURIComponent(workId)}`,
    library: `${root}/library`,
    usage: `${root}/usage`,
  };
}

/** The entry a shared page renders in: its page frame, the page a signed-in
 * person lands on, and where it serves the person's own pages. */
export type EntryShell = { Layout: ComponentType<PageLayoutProps>; home: string; personal: PersonalPaths };

export const EntryShellContext = createContext<EntryShell | null>(null);

export function useEntryShell() {
  const value = useContext(EntryShellContext);
  if (!value) throw new Error("A shared page requires an EntryShellContext");
  return value;
}
