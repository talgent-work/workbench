import { Code, ConnectError, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { refreshSession } from "./session";

// An Identity Token lives fifteen minutes. When the gateway rejects one, ask
// Account for a fresh token once and repeat the call. If Account rejects the
// session, the error reaches AuthGate, which sends the person to sign in; if
// Account cannot be reached, the call fails as unavailable so the page offers
// a retry instead. A stream cannot replay its request, so it only refreshes
// for its reconnect.
export const refreshOnUnauthenticated: Interceptor = (next) => async (request) => {
  try {
    return await next(request);
  } catch (error) {
    if (!(error instanceof ConnectError) || error.code !== Code.Unauthenticated) throw error;
    const outcome = await refreshSession();
    if (outcome === "unavailable") throw new ConnectError("session refresh unavailable", Code.Unavailable, undefined, undefined, error);
    if (outcome === "rejected" || request.stream) throw error;
    return next(request);
  }
};

export const transport = createConnectTransport({
  baseUrl: "/api",
  interceptors: [refreshOnUnauthenticated],
});

export const streamTransport = transport;
export const traceTransport = transport;
