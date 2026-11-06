import type { JsonObject, JsonValue } from "@bufbuild/protobuf";
import type { InstalledPlugin, ParameterDeclaration } from "@talgent/workbench-contracts/marketplace/public/v1/marketplace_pb";

// Form values are text as typed; they become typed manual values only after
// validation against the release's declarations. Blank means "not set here",
// so a declared default or a saved value continues to apply.
export type ParameterDrafts = Record<string, string>;
export type ParameterErrors = Record<string, string>;

export const hasDefault = (declaration: ParameterDeclaration) => declaration.hasDefault || declaration.default !== undefined;
export const parameterLabel = (name: string, declaration: ParameterDeclaration) => declaration.title || name;

// A value that must be supplied: required, no declared default and nothing saved.
export function missingRequired(declarations: Record<string, ParameterDeclaration>, saved: { configuration?: JsonObject; redacted?: string[] }, drafts: ParameterDrafts = {}) {
  return Object.entries(declarations).filter(([name, declaration]) => declaration.required && !hasDefault(declaration)
    && !drafts[name]?.trim() && saved.configuration?.[name] === undefined && !saved.redacted?.includes(name)).map(([name]) => name);
}

// Missing required settings on an existing installation, such as after a new
// release declares another required parameter.
export function installationNeedsSetup(declarations: Record<string, ParameterDeclaration>, installed: InstalledPlugin) {
  return missingRequired(declarations, { configuration: installed.configuration, redacted: installed.redactedParameters }).length > 0;
}

export function parseParameters(declarations: Record<string, ParameterDeclaration>, drafts: ParameterDrafts, saved: { configuration?: JsonObject; redacted?: string[] } = {}): { values: JsonObject; errors: ParameterErrors } {
  const values: JsonObject = {};
  const errors: ParameterErrors = {};
  for (const name of missingRequired(declarations, saved, drafts)) errors[name] = `Enter ${parameterLabel(name, declarations[name]).toLowerCase()}.`;
  for (const [name, declaration] of Object.entries(declarations)) {
    const raw = drafts[name];
    if (raw === undefined || !raw.trim() || errors[name]) continue;
    const parsed = parseValue(declaration, raw);
    if ("error" in parsed) errors[name] = parsed.error;
    else values[name] = parsed.value;
  }
  return { values, errors };
}

function parseValue(declaration: ParameterDeclaration, raw: string): { value: JsonValue } | { error: string } {
  if (declaration.multiple) {
    const items = raw.split("\n").map(item => item.trim()).filter(Boolean);
    return items.length ? { value: items } : { error: "Enter at least one value." };
  }
  if (declaration.type === "number") {
    const number = Number(raw);
    if (!Number.isFinite(number)) return { error: "Enter a number." };
    if (declaration.min !== undefined && number < declaration.min) return { error: `Enter ${declaration.min} or more.` };
    if (declaration.max !== undefined && number > declaration.max) return { error: `Enter ${declaration.max} or less.` };
    return { value: number };
  }
  if (declaration.type === "boolean") return { value: raw === "true" };
  if (declaration.options.length && !declaration.options.includes(raw)) return { error: "Choose one of the options." };
  return { value: raw };
}

// Saved values shown back in a form. Sensitive values are never returned by
// ordinary queries, so they stay blank with a "saved" placeholder.
export function draftsFrom(configuration: JsonObject | undefined): ParameterDrafts {
  const drafts: ParameterDrafts = {};
  for (const [name, value] of Object.entries(configuration ?? {})) {
    drafts[name] = Array.isArray(value) ? value.join("\n") : String(value);
  }
  return drafts;
}
