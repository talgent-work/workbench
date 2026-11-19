// Marketplace is pages only; depth lives in these paths and the topbar trail.
const encode = encodeURIComponent;
export const marketplacePaths = {
  plugins: "/marketplace",
  pluginCategory: (category: string) => `/marketplace/plugins/categories/${encode(category)}`,
  plugin: (slug: string) => `/marketplace/plugins/${encode(slug)}`,
  connectors: "/marketplace/connectors",
  connectorCategory: (category: string) => `/marketplace/connectors/categories/${encode(category)}`,
  connector: (id: string) => `/marketplace/connectors/${encode(id)}`,
  providers: "/marketplace/providers",
  provider: (id: string) => `/marketplace/providers/${encode(id)}`,
};

// The Workshop: what a person makes and publishes, reached from the account menu.
export const workshopPaths = {
  list: "/workshop",
  newConnector: "/workshop/connectors/new",
  connector: (id: string) => `/workshop/connectors/${encode(id)}`,
};
