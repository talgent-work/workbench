import { ModelVendor } from "@talgent/contracts/agent/public/v1/model_pb";

// Each Model Vendor the platform knows has a bundled mark; see product-icons/README.md.
export const modelVendors: Record<ModelVendor, { label: string; icon?: string }> = {
  [ModelVendor.UNSPECIFIED]: { label: "Model" },
  [ModelVendor.ANTHROPIC]: { label: "Anthropic", icon: "anthropic" },
  [ModelVendor.OPENAI]: { label: "OpenAI", icon: "openai" },
  [ModelVendor.DEEPSEEK]: { label: "DeepSeek", icon: "deepseek" },
  [ModelVendor.ZHIPU]: { label: "Zhipu", icon: "glm" },
  [ModelVendor.KIMI]: { label: "Kimi", icon: "kimi" },
  [ModelVendor.QWEN]: { label: "Qwen", icon: "qwen" },
  [ModelVendor.MINIMAX]: { label: "MiniMax", icon: "minimax" },
};
