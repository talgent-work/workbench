// The authorization server of a per-target OAuth Provider in Marketplace's
// credential supply tests: one authorization code flow with PKCE grants every
// resource the client names (RFC 8707), and each refresh_token grant naming
// one resource gets an access token for that resource alone.
import Provider, { errors } from "oidc-provider";

// Each resource server, by the resource indicator it is known by, and the
// client it authenticates with to introspect tokens.
const resources = { "https://docs.example.com": "service-docs", "https://tickets.example.com": "service-tickets" };
const secret = "fixture-only";

const provider = new Provider("http://oidc-provider.test", {
  clients: [
    { client_id: "workbench", client_secret: secret, redirect_uris: ["http://workbench.test/profile"], grant_types: ["authorization_code", "refresh_token"], response_types: ["code"] },
    ...Object.values(resources).map(client_id => ({ client_id, client_secret: secret, redirect_uris: [], grant_types: [], response_types: [] })),
  ],
  pkce: { required: () => true },
  // Every person who signs in is an account named by the login they enter.
  findAccount: (_, sub) => ({ accountId: sub, claims: () => ({ sub }) }),
  // The person consents by signing in, so no consent page follows.
  loadExistingGrant: async ctx => {
    const existing = ctx.oidc.session.grantIdFor(ctx.oidc.client.clientId);
    if (existing) return ctx.oidc.provider.Grant.find(existing);
    const grant = new ctx.oidc.provider.Grant({ clientId: ctx.oidc.client.clientId, accountId: ctx.oidc.session.accountId });
    grant.addOIDCScope("openid");
    for (const resource of Object.keys(resources)) grant.addResourceScope(resource, "api");
    await grant.save();
    return grant;
  },
  issueRefreshToken: async (_, client) => client.grantTypeAllowed("refresh_token"),
  ttl: { AccessToken: 900, AuthorizationCode: 60, Grant: 86400, IdToken: 900, Interaction: 600, RefreshToken: 86400, Session: 86400 },
  features: {
    devInteractions: { enabled: true },
    revocation: { enabled: true },
    introspection: {
      enabled: true,
      // A resource server learns of a token only when it is in the token's audience.
      allowedPolicy: async (ctx, client, token) => token.clientId === client.clientId || (token.aud !== undefined && resources[token.aud] === client.clientId),
    },
    resourceIndicators: {
      enabled: true,
      // A request naming no resource gets a token for the userinfo endpoint.
      defaultResource: () => undefined,
      useGrantedResource: () => false,
      getResourceServerInfo: (_, resource) => {
        if (!resources[resource]) throw new errors.InvalidTarget();
        return { scope: "api", audience: resource, accessTokenFormat: "opaque", accessTokenTTL: 900 };
      },
    },
  },
});
provider.listen(3000);
