import { useId, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button } from "@heroui/react";
import { ChevronDown } from "lucide-react";
import { InteractionPhase, UnifiedMessageKind, type UnifiedMessage } from "@talgent/contracts/dispatcher/public/v1/unified_message_pb";
import { useWorkStreamSource } from "../../lib/work-stream-source";
import { parseWorkQuestions } from "../../lib/work-questions";
import { ToolMessageDetail } from "./tool-message-detail";

export function WorkQuestionRecord({ message, action, output, open, onToggle }: {
  message: UnifiedMessage; action?: UnifiedMessage; output?: UnifiedMessage; open: boolean; onToggle: () => void;
}) {
  const contentId = useId();
  const [showOutput, setShowOutput] = useState(false);
  if (message.payload.case !== "interaction") return null;
  const question = message.payload.value;
  let answers: Record<string, string> = {};
  let valid = true;
  try {
    const value: unknown = JSON.parse(question.responseJson || "{}");
    if (value && typeof value === "object" && !Array.isArray(value) && Object.values(value).every(answer => typeof answer === "string")) answers = value as Record<string, string>;
    else valid = false;
  } catch { valid = false; }
  const answered = Object.keys(answers).length;
  const asked = question.questionCount ? `Asked ${question.questionCount} ${question.questionCount === 1 ? "question" : "questions"}` : "Asked questions";
  const resolved = valid && (question.phase === InteractionPhase.RESPONDED || question.phase === InteractionPhase.SKIPPED);
  const status = resolved ? `Answered ${answered}${question.questionCount ? `, skipped ${Math.max(0, question.questionCount - answered)}` : ""}`
    : !valid ? "Response unavailable" : question.phase === InteractionPhase.FAILED ? "Response failed" : question.phase === InteractionPhase.CANCELLED ? "Cancelled" : "Waiting for response";
  const failedOutput = output?.payload.case === "observation" && output.payload.value.isError ? output : undefined;
  const cancelled = question.phase === InteractionPhase.CANCELLED || question.phase === InteractionPhase.SKIPPED;
  return <div className="wm-question-record" id={`wm-message-${message.id}`} data-message-id={message.id}>
    <Button variant="ghost" className="wm-tool-summary" aria-expanded={open} aria-controls={contentId} onPress={onToggle}>{asked} · {status}<ChevronDown size={14} aria-hidden="true" /></Button>
    {open && <div id={contentId}>
      {question.error && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{question.error}</Alert.Description></Alert.Content></Alert>}
      {failedOutput && <Button size="sm" variant="ghost" aria-expanded={showOutput} onPress={() => setShowOutput(!showOutput)}>{showOutput ? "Show questions" : cancelled ? "Show tool output" : "Show tool error"}</Button>}
      {failedOutput && showOutput ? <ToolMessageDetail workId={message.agentWorkId} messageId={failedOutput.id} /> : <QuestionAnswers message={message} action={action} answers={answers} resolved={resolved} />}
    </div>}
  </div>;
}

function QuestionAnswers({ message, action, answers, resolved }: {
  message: UnifiedMessage; action?: UnifiedMessage; answers: Record<string, string>; resolved: boolean;
}) {
  const streamClient = useWorkStreamSource();
  const toolCallId = message.payload.case === "interaction" ? message.payload.value.toolCallId : "";
  const input = useQuery({
    queryKey: ["question-input", message.agentWorkId, toolCallId, action?.id],
    queryFn: async ({ signal }) => {
      // Only the expanded record owns its original input. It is released on
      // collapse; each transport page stays within the detail API limit.
      const original = action ?? (await streamClient.listMessagesBySession({ agentWorkId: message.agentWorkId, agent: message.agent, toolCallIds: [toolCallId], kinds: [UnifiedMessageKind.ACTION], limit: 1 }, { signal })).messages[0];
      if (!original) throw new Error("Original questions unavailable");
      let text = "", offset = 0;
      for (;;) {
        const page = await streamClient.getMessageDetail({ agentWorkId: message.agentWorkId, messageId: original.id, offset, limit: 65536 }, { signal });
        text += page.text;
        if (page.nextOffset >= page.totalLength) break;
        if (page.nextOffset <= offset) throw new Error("Incomplete question input");
        offset = page.nextOffset;
      }
      const parsed = parseWorkQuestions(text);
      if (!parsed) throw new Error("Original questions unavailable");
      return parsed.questions;
    },
    gcTime: 0, staleTime: Infinity, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false,
  });
  return <>
    {input.isPending && <p className="sr-only" role="status">Loading questions…</p>}
    {input.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Could not load original questions.</Alert.Description></Alert.Content><Button variant="ghost" size="sm" onPress={() => void input.refetch()}>Retry</Button></Alert>}
    {input.data && <dl>{input.data.map((question, index) => <div key={index}><dt>{question.question}</dt><dd>{Object.hasOwn(answers, question.question) ? answers[question.question] : resolved ? "Skipped" : "No answer"}</dd></div>)}</dl>}
  </>;
}
