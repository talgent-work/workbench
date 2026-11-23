import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { ModelVendor } from "@talgent/contracts/agent/public/v1/model_pb";
import { PromptModelControl } from "../components/composer/prompt-bar-controls";
import type { StartModel } from "../lib/work-model";

const model = (configurationId: string, name: string, vendor: ModelVendor, defaultThinkingLevel: string): StartModel =>
  ({ configurationId, machineLogin: false, name, vendor, thinkingLevels: ["low", "medium", "high", "max"], defaultThinkingLevel });
const deepseek = model("deepseek", "DeepSeek Flash", ModelVendor.DEEPSEEK, "high");
const claude = model("claude", "Claude", ModelVendor.ANTHROPIC, "medium");

it("takes the chosen model's own default thinking level, not the one chosen for the previous model", async () => {
  const onChange = vi.fn();
  render(<PromptModelControl models={[deepseek, claude]} model={deepseek} thinking="max" open onOpenChange={() => undefined} onChange={onChange} />);

  await userEvent.click(screen.getByRole("option", { name: /Claude/ }));
  expect(onChange).toHaveBeenCalledWith(claude, "medium");
});
