import { useEntryShell, type PageLayoutProps } from "./entry-shell";

/** The current entry's page frame. */
export function PageLayout(props: PageLayoutProps) {
  const { Layout } = useEntryShell();
  return <Layout {...props} />;
}
