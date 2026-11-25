// The browser side of Account's Login Session. The SPA never sees a token:
// Account sets HttpOnly cookies, and these calls only ask it to refresh or end
// the session of this device.

const jsonPost: RequestInit = {
  method: "POST",
  credentials: "same-origin",
  headers: { "Content-Type": "application/json" },
  body: "{}",
};

const maxNextLength = 2048;

/**
 * An internal Portal path, or "/" for anything that could leave the site or
 * loop back into sign-in. Mirrors internalPath in Account's /auth handler,
 * which re-checks it; this copy keeps the browser from building such links.
 */
export function safeInternalPath(value: string | null | undefined): string {
  const path = value?.trim() ?? "";
  if (!path.startsWith("/") || path.startsWith("//") || path.length > maxNextLength || /[\\\r\n\t]/.test(path)) return "/";
  const pathname = path.split(/[?#]/, 1)[0];
  if (pathname === "/login" || pathname === "/auth" || pathname.startsWith("/auth/")) return "/";
  return path;
}

/** Where the "Continue with GitHub" action starts a sign-in. GitHub returns
 * every sign-in to one entry's host, so the sign-in names this entry's origin
 * to come back to; Account returns only to the entries it trusts. */
export function signInHref(next: string): string {
  return `/auth/github/login?next=${encodeURIComponent(`${window.location.origin}${safeInternalPath(next)}`)}`;
}

/** The Portal sign-in page, returning to next afterwards. home is where the
 * entry lands after sign-in anyway, so naming it adds nothing. */
export function signInPagePath(next: string, home = "/"): string {
  const target = safeInternalPath(next);
  return target === "/" || target === home ? "/login" : `/login?next=${encodeURIComponent(target)}`;
}

/** refreshed: a new token is set. rejected: sign in again. unavailable: try later. */
export type RefreshOutcome = "refreshed" | "rejected" | "unavailable";

let inFlight: Promise<RefreshOutcome> | undefined;

// Once Account rejects a refresh, this device's Login Session is over: Account
// has cleared its cookies, and only signing in again, which reloads the page,
// starts another. Pages learn it at once instead of at their next session
// check, and no call asks Account again meanwhile.
let ended = false;
const endListeners = new Set<() => void>();

export function sessionEnded(): boolean {
  return ended;
}

export function subscribeSessionEnd(listener: () => void): () => void {
  endListeners.add(listener);
  return () => endListeners.delete(listener);
}

function endSession() {
  if (ended) return;
  ended = true;
  for (const listener of endListeners) listener();
}

async function requestRefreshOnce(): Promise<RefreshOutcome> {
  if (ended) return "rejected";
  let response: Response;
  try {
    response = await fetch("/auth/refresh", jsonPost);
  } catch {
    return "unavailable";
  }
  if (response.ok) return "refreshed";
  if (response.status !== 401) return "unavailable";
  endSession();
  return "rejected";
}

// Account may have rotated the secret although its response never arrived.
// It gives the same new secret back to the replaced one only within its
// replay window (30 seconds by default), so retries stay well inside it;
// a later retry would present a replaced secret and end the session.
const refreshRetryDelaysMs = [1_000, 3_000];

async function requestRefresh(): Promise<RefreshOutcome> {
  let outcome = await requestRefreshOnce();
  for (const delay of refreshRetryDelaysMs) {
    if (outcome !== "unavailable") break;
    await new Promise(resolve => setTimeout(resolve, delay));
    outcome = await requestRefreshOnce();
  }
  return outcome;
}

// Tabs of this entry share one refresh cookie, so the lock lets one refresh
// at a time and the next sends the cookie the first received. Other entries
// on sibling hosts share the cookie too but not the lock; Account replays a
// just-replaced secret for them, so presenting it again stays safe.
async function refreshAcrossTabs(): Promise<RefreshOutcome> {
  const locks = typeof navigator !== "undefined" ? navigator.locks : undefined;
  if (!locks) return requestRefresh();
  return locks.request("talgent-session-refresh", requestRefresh);
}

/** Refreshes this device's session once, however many callers ask at once. */
export function refreshSession(): Promise<RefreshOutcome> {
  if (ended) return Promise.resolve("rejected");
  inFlight ??= refreshAcrossTabs().finally(() => {
    inFlight = undefined;
  });
  return inFlight;
}

/**
 * Ends this device's Login Session; other devices stay signed in. Fails when
 * Account could not revoke it, and the device then stays signed in.
 */
export async function signOut(): Promise<void> {
  const response = await fetch("/auth/logout", jsonPost);
  if (!response.ok) throw new Error(`sign-out failed with ${response.status}`);
}
