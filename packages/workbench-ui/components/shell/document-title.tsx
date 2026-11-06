import { useEffect, type ReactNode } from "react";

const titleSuffix = "Talgent";

export function DocumentTitle({ title, children }: { title: string; children: ReactNode }) {
  useEffect(() => {
    document.title = `${title} | ${titleSuffix}`;
  }, [title]);

  return <>{children}</>;
}
