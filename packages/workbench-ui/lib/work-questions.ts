export interface WorkQuestion {
  question: string;
  options?: { label: string; description?: string }[];
  multiSelect?: boolean;
}

export function parseWorkQuestions(json: string): { input: Record<string, unknown>; questions: WorkQuestion[] } | null {
  try {
    const input = JSON.parse(json);
    if (!input || typeof input !== "object" || !Array.isArray(input.questions) || !input.questions.length) return null;
    if (!input.questions.every((question: unknown) => {
      if (!question || typeof question !== "object" || !("question" in question) || typeof question.question !== "string") return false;
      if (!("options" in question)) return true;
      return Array.isArray(question.options) && question.options.every(option => option && typeof option === "object" && typeof option.label === "string");
    })) return null;
    return { input, questions: input.questions };
  } catch { return null; }
}
