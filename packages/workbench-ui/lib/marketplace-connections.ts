import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { marketplaceClient } from "./client";
import { useCurrentUser } from "./account-service";
import { marketplaceQueryKey } from "./marketplace-catalog";
import { notify } from "./notifications";

// Provider authorization is a round trip through the Provider. The GitHub App
// registers /profile as its callback URL, so the redirect always returns there;
// the page that started the connection is remembered and restored afterwards.
// Removal condition: once the App's callback URL is the Provider page, pass
// that page as the redirect and drop the remembered return path.
export const providerCallbackPath = "/profile";
const returnKey = "talgent.providerAuthorization.return";

export const providerNames: Record<string, string> = { github: "GitHub" };
export const providerName = (provider: string) => providerNames[provider] ?? provider;
// Providers supported by the platform. Only GitHub exists today.
export const supportedProviders = ["github"] as const;

const callbackURI = () => `${window.location.origin}${providerCallbackPath}`;

/** The connection's stored status only, without asking the Provider what it reaches. */
export function useProviderConnection(provider: string) {
  const userId = useCurrentUser().data?.user?.id ?? "";
  return useQuery({ queryKey: [...marketplaceQueryKey, "provider-state", provider, userId], enabled: Boolean(userId), retry: false,
    queryFn: ({ signal }) => marketplaceClient.getProviderConnectionState({ provider }, { signal }) });
}

const providerAccessKey = (provider: string, userId: string) => [...marketplaceQueryKey, "provider", provider, userId];

/** The connection and its reach (accounts, permissions) as last seen; reading never asks the Provider. */
export function useProviderAccess(provider: string) {
  const userId = useCurrentUser().data?.user?.id ?? "";
  return useQuery({ queryKey: providerAccessKey(provider, userId), enabled: Boolean(userId), retry: false,
    queryFn: ({ signal }) => marketplaceClient.getProviderAccess({ provider }, { signal }) });
}

/** Asks the Provider now what the authorization reaches; the page then shows the result. */
export function useRefreshProviderAccess(provider: string) {
  const userId = useCurrentUser().data?.user?.id ?? "";
  const cache = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const response = await marketplaceClient.refreshProviderAccess({ provider });
      if (!response.access) throw new Error("The refreshed access is missing");
      return response.access;
    },
    onSuccess: access => {
      cache.setQueryData(providerAccessKey(provider, userId), access);
      return cache.invalidateQueries({ queryKey: ["work-start-repositories"] });
    },
    onError: () => notify(`provider:${provider}`, "danger", `${providerName(provider)} couldn’t be checked`, "Try again in a moment."),
  });
}

// Starts connecting; the browser leaves for the Provider on success.
export function useConnectProvider() {
  return useMutation({
    mutationFn: async ({ provider, returnTo }: { provider: string; returnTo: string }) => {
      const response = await marketplaceClient.beginProviderAuthorization({ provider, redirectUri: callbackURI() });
      if (!response.authorizationUrl) throw new Error("The authorization address is missing");
      window.sessionStorage.setItem(returnKey, returnTo);
      return response.authorizationUrl;
    },
    onSuccess: url => window.location.assign(url),
    onError: (_, { provider }) => notify(`provider:${provider}`, "danger", `${providerName(provider)} couldn’t be opened`, "Try connecting again."),
  });
}

// The page that started connecting, read once.
export function takeProviderReturn(provider: string) {
  const returnTo = window.sessionStorage.getItem(returnKey) || `/marketplace/providers/${provider}`;
  window.sessionStorage.removeItem(returnKey);
  return returnTo;
}

// Completes a returned authorization and reports where the user started.
export async function completeProviderAuthorization(provider: string, code: string, state: string) {
  const response = await marketplaceClient.completeProviderAuthorization({ provider, code, state, redirectUri: callbackURI() });
  if (!response.authorization) throw new Error("The authorization is missing");
  return { authorization: response.authorization, returnTo: takeProviderReturn(provider) };
}

// Disconnecting asks the Provider to revoke; success means it was confirmed.
export function useDisconnectProvider() {
  const cache = useQueryClient();
  return useMutation({
    mutationFn: async (authorizationId: string) => {
      const response = await marketplaceClient.cancelPersonalAuthorization({ authorizationId });
      if (response.authorization?.id !== authorizationId || response.authorization.status !== "revoked") throw new Error("Revocation was not confirmed");
    },
    onSettled: () => Promise.all([cache.invalidateQueries({ queryKey: marketplaceQueryKey }), cache.invalidateQueries({ queryKey: ["work-start-options"] }), cache.invalidateQueries({ queryKey: ["work-start-repositories"] })]),
  });
}

// Display names for services whose catalog name is a lowercase identifier.
const serviceNames: Record<string, string> = { github: "GitHub", talgent: "Talgent" };
export const serviceLabel = (name: string) => serviceNames[name] ?? name.split(/[-_]/).map(word => word.charAt(0).toUpperCase() + word.slice(1)).join(" ");
