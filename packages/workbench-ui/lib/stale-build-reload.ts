// Each image serves only its own content-hashed assets, so after a release a
// page still running the previous build asks for chunks that no longer exist
// (the relation graph, Mermaid, a code language). Vite reports that failure as
// `vite:preloadError`; reloading picks up the current build. A failure right
// after such a reload is a real outage, not a stale page, so it is left to
// surface instead of reloading in a loop.
const storageKey = "talgent:stale-build-reload";
const retryAfterMs = 10_000;

type ReloadTarget = Pick<Window, "addEventListener" | "removeEventListener" | "sessionStorage"> & { location: Pick<Location, "reload"> };

export function installStaleBuildReload(target: ReloadTarget = window, now: () => number = Date.now) {
  const onPreloadError = (event: Event) => {
    const last = readLast(target);
    if (last !== null && now() - last < retryAfterMs) return;
    // Without storage a reload could not be told from the next one, so it could loop.
    if (!writeLast(target, now())) return;
    event.preventDefault();
    target.location.reload();
  };
  target.addEventListener("vite:preloadError", onPreloadError);
  return () => target.removeEventListener("vite:preloadError", onPreloadError);
}

function readLast(target: ReloadTarget): number | null {
  try {
    const value = Number(target.sessionStorage.getItem(storageKey));
    return Number.isFinite(value) && value > 0 ? value : null;
  } catch {
    return null;
  }
}

function writeLast(target: ReloadTarget, at: number) {
  try {
    target.sessionStorage.setItem(storageKey, String(at));
    return true;
  } catch {
    return false;
  }
}
