import { useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useLocation, useNavigate } from "react-router-dom";
import { marketplaceQueryKey } from "../../lib/marketplace-catalog";
import { completeProviderAuthorization, providerName, takeProviderReturn } from "../../lib/marketplace-connections";
import { notify } from "../../lib/notifications";

// Completes a Provider authorization returning on the registered callback URL,
// then returns to the page that started it. The OAuth code is used once.
export function useProviderAuthorizationCallback() {
  const handled = useRef(false);
  const navigate = useNavigate();
  const cache = useQueryClient();
  const params = new URLSearchParams(useLocation().search);
  const code = params.get("code") ?? "", state = params.get("state") ?? "";
  // The Provider returns `error` instead of a code when the user declines.
  const declined = Boolean(params.get("error") && state);
  const pending = Boolean(code && state) || declined;
  useEffect(() => {
    if (!pending || handled.current) return;
    handled.current = true;
    const provider = "github";
    const name = providerName(provider);
    if (declined) {
      notify(`provider:${provider}`, "warning", `${name} wasn’t connected`, "Authorization was cancelled. You can connect again at any time.");
      navigate(takeProviderReturn(provider), { replace: true });
      return;
    }
    notify(`provider:${provider}`, "default", `Connecting ${name}…`, undefined, { timeout: 0 });
    completeProviderAuthorization(provider, code, state).then(({ authorization, returnTo }) => {
      void cache.invalidateQueries({ queryKey: marketplaceQueryKey });
      void cache.invalidateQueries({ queryKey: ["work-start-options"] });
      // A new authorization reaches different repositories.
      void cache.invalidateQueries({ queryKey: ["work-start-repositories"] });
      notify(`provider:${provider}`, "success", `${name} connected`, `Signed in as ${authorization.providerLogin}. Works you start from now on can use it.`);
      navigate(returnTo, { replace: true });
    }, () => {
      notify(`provider:${provider}`, "danger", `${name} couldn’t be connected`, "Try connecting again.");
      navigate(takeProviderReturn(provider), { replace: true });
    });
  }, [pending, declined, code, state, navigate, cache]);
  return pending;
}
