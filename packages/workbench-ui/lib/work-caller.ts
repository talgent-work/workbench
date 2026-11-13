/** The party in whose name a Work was started: a person, an Application, or
 * an object of a kind an extension defines. The trace API names an Extension
 * Work by its Work Caller and a Standalone Work without one. */
export type WorkCaller = { kind: string; id: string };

/** The Work Caller kinds the core defines, whose Works are Standalone Works;
 * every other kind is an extension's. */
const standaloneKinds = new Set(["person", "application"]);

/** Whether a Work under this Work Caller is an Extension Work. */
export function isExtensionWorkCaller(caller: { kind: string } | null | undefined): boolean {
  return caller ? caller.kind !== "" && !standaloneKinds.has(caller.kind) : false;
}
