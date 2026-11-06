import { create } from "@bufbuild/protobuf";
import { PluginReleaseSchema } from "@talgent-work/workbench-contracts/marketplace/public/v1/marketplace_pb";
import { describe, expect, it } from "vitest";
import { pluginPresentation } from "../lib/marketplace-plugins";

const bytes = (value: unknown) => new TextEncoder().encode(JSON.stringify(value));

describe("native catalog presentation", () => {
  it("uses the selected server release instead of a locally bundled catalog", () => {
    const plugin = create(PluginReleaseSchema, { slug: "third-party-tools", nativeEntry: bytes({ displayName: "Team tools", category: "productivity", author: { name: "Acme" }, license: "MIT", homepage: "https://acme.test", keywords: ["team", 3] }) });
    expect(pluginPresentation(plugin)).toEqual({ name: "Team tools", category: "Productivity", author: "Acme", license: "MIT", homepage: "https://acme.test", keywords: ["team"] });
    plugin.nativeEntry = new TextEncoder().encode("broken");
    expect(pluginPresentation(plugin)).toMatchObject({ name: "Third Party Tools", category: "Other", author: "", keywords: [] });
  });
  it("only links secure homepages", () => {
    const plugin = create(PluginReleaseSchema, { slug: "tools", nativeEntry: bytes({ homepage: "javascript:alert(1)" }) });
    expect(pluginPresentation(plugin).homepage).toBe("");
  });
});
