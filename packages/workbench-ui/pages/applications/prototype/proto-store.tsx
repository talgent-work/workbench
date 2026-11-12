// PROTOTYPE (Applications): holds the in-memory Applications for every variant.
import type { ReactNode } from "react";
import { StoreContext, useStoreState } from "./proto-data";

export function ProtoStoreProvider({ children }: { children: ReactNode }) {
  return <StoreContext value={useStoreState()}>{children}</StoreContext>;
}
