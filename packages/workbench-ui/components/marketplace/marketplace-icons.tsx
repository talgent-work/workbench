import { BookOpen, Code2, Database, FlaskConical, Palette, Plug, Puzzle, Rocket, ShieldCheck, Sparkles, Workflow, type LucideIcon } from "lucide-react";
import figma from "../../assets/brand-icons/figma.svg";
import github from "../../assets/brand-icons/github.svg";
import linear from "../../assets/brand-icons/linear.svg";
import notion from "../../assets/brand-icons/notion.svg";
import sentry from "../../assets/brand-icons/sentry.svg";
import slack from "../../assets/brand-icons/slack.svg";
import { pluginPresentation, type PresentablePlugin } from "../../lib/marketplace-plugins";

// Known services use their brand mark; Talgent's own Plugins and Connectors use
// the product mark; every other Plugin gets a glyph tinted by its category.
const brands: Record<string, { url: string; color: string }> = {
  github: { url: github, color: "#181717" }, linear: { url: linear, color: "#5E6AD2" }, notion: { url: notion, color: "#000000" },
  slack: { url: slack, color: "#4A154B" }, figma: { url: figma, color: "#F24E1E" }, sentry: { url: sentry, color: "#362D59" },
  talgent: { url: "/assets/brand/talgent-mark.png", color: "var(--brand-foreground)" },
};

// A name belongs to a brand when it is the brand or a hyphenated extension of it.
function brandOf(name: string) {
  const lower = name.toLowerCase();
  return Object.keys(brands).find(brand => lower === brand || lower.startsWith(`${brand}-`) || lower.endsWith(`-${brand}`));
}

const categories: Record<string, { hue: string; glyph: LucideIcon }> = {
  Development: { hue: "oklch(55% 0.09 250)", glyph: Code2 },
  Productivity: { hue: "oklch(52% 0.09 285)", glyph: Workflow },
  Security: { hue: "oklch(55% 0.1 20)", glyph: ShieldCheck },
  Testing: { hue: "oklch(55% 0.09 150)", glyph: FlaskConical },
  Database: { hue: "oklch(55% 0.08 190)", glyph: Database },
  Deployment: { hue: "oklch(58% 0.1 45)", glyph: Rocket },
  Design: { hue: "oklch(55% 0.11 330)", glyph: Palette },
  Learning: { hue: "oklch(58% 0.09 75)", glyph: BookOpen },
  Other: { hue: "oklch(55% 0 0)", glyph: Puzzle },
};

function Tile({ size, hue, brand, children }: { size: number; hue?: string; brand?: boolean; children: React.ReactNode }) {
  return <span className={`mk-icon${brand ? " mk-icon-brand" : ""}`} style={{ width: size, height: size, borderRadius: Math.round(size * 0.28), ["--mk-hue" as string]: hue ?? categories.Other.hue }} aria-hidden="true">{children}</span>;
}

function BrandMark({ id, size }: { id: string; size: number }) {
  const brand = brands[id];
  return <span className="mk-brand-mark" style={{ width: size, height: size, backgroundColor: brand.color, maskImage: `url("${brand.url}")`, WebkitMaskImage: `url("${brand.url}")` }} />;
}

export function PluginIcon({ plugin, size = 40 }: { plugin: PresentablePlugin; size?: number }) {
  const brand = plugin.source === "talgent" ? "talgent" : brandOf(plugin.slug);
  if (brand) return <Tile size={size} brand><BrandMark id={brand} size={Math.round(size * 0.5)} /></Tile>;
  const category = categories[pluginPresentation(plugin).category] ?? { hue: categories.Other.hue, glyph: Sparkles };
  const Glyph = category.glyph;
  return <Tile size={size} hue={category.hue}><Glyph size={Math.round(size * 0.48)} strokeWidth={1.7} /></Tile>;
}

export function ConnectorIcon({ name, size = 40 }: { name: string; size?: number }) {
  const brand = brandOf(name);
  return <span className="mk-icon-stack" style={{ width: size, height: size }} aria-hidden="true">
    {brand ? <Tile size={size} brand><BrandMark id={brand} size={Math.round(size * 0.5)} /></Tile>
      : <Tile size={size} hue="oklch(57% 0.09 70)"><span className="mk-icon-letter" style={{ fontSize: Math.round(size * 0.4) }}>{name.charAt(0).toUpperCase()}</span></Tile>}
    {size >= 28 && <span className="mk-icon-badge"><Plug size={Math.max(10, Math.round(size * 0.26))} /></span>}
  </span>;
}

export function ProviderIcon({ id, size = 40 }: { id: string; size?: number }) {
  const brand = brandOf(id);
  return brand ? <Tile size={size} brand><BrandMark id={brand} size={Math.round(size * 0.5)} /></Tile> : <Tile size={size}><span className="mk-icon-letter">{id.charAt(0).toUpperCase()}</span></Tile>;
}

