import { useEffect, useId, useRef, useState } from "react";
import { Alert, Button, Tooltip } from "@heroui/react";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, ChevronLeft, ChevronRight, CornerDownLeft, MessageCircleQuestion, Pencil, Terminal, X } from "lucide-react";
import { MessageDetailFormat, UnifiedMessageKind, type PendingInteraction } from "@talgent-work/workbench-contracts/dispatcher/public/v1/unified_message_pb";
import { agentWorkClient } from "../../lib/client";
import { useWorkStreamSource } from "../../lib/work-stream-source";
import { parseWorkQuestions } from "../../lib/work-questions";
import { ToolMessageDetail } from "./tool-message-detail";
import "./work-interaction.css";

export function WorkInteraction({ workId, request, disabled = false, onResponded }: { workId: string; request: PendingInteraction; disabled?: boolean; onResponded: () => void }) {
  const [pending, setPending] = useState(false);
  const [sent, setSent] = useState(false);
  const [error, setError] = useState("");
  const inFlight = useRef(false);
  const allowButton = useRef<HTMLButtonElement>(null);
  useEffect(() => { if (!request.replySubmitted) allowButton.current?.focus({ preventScroll: true }); }, [request.replySubmitted]);
  const locked = disabled || pending || sent || request.replySubmitted;
  const respond = async (allow: boolean, alwaysAllow = false, updatedInputJson = "") => {
    if (locked || inFlight.current) return;
    inFlight.current = true; setPending(true); setError("");
    try {
      await agentWorkClient.respondToInteraction({ workId, interactionId: request.id, allow, alwaysAllow, updatedInputJson });
      setSent(true); onResponded();
    } catch (err) { setError(err instanceof Error ? err.message : "Response could not be sent. Try again."); }
    finally { inFlight.current = false; setPending(false); }
  };
  const feedback = <>{error && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{error}</Alert.Description></Alert.Content></Alert>}{(sent || request.replySubmitted) && <p className="wi-feedback" role="status">Response sent. Waiting for Work to continue…</p>}</>;
  if (request.toolName === "AskUserQuestion") return <WorkQuestion request={request} disabled={locked} pending={pending} respond={respond} feedback={feedback} />;
  return <section className="wi-card wi-approval" aria-label="Approval request" onKeyDown={event => { if (event.key === "Escape" && !locked) { event.preventDefault(); event.stopPropagation(); void respond(false); } }}>
    <header className="wi-heading"><span><Terminal size={18} />{request.toolName === "Bash" ? "Terminal" : request.toolName}</span></header>
    <h3>{request.prompt || `Allow ${request.toolName}?`}</h3>
    {request.toolCallId && <ApprovalOperation workId={workId} toolCallId={request.toolCallId} />}
    <footer className="wi-approval-footer">
      <Button size="sm" variant="outline" isDisabled={locked} onPress={() => void respond(false)}>Deny <kbd>Esc</kbd></Button>
      {request.canAlwaysAllow && <Tooltip><Button size="sm" variant="outline" isDisabled={locked} onPress={() => void respond(true, true)}>Always allow</Button><Tooltip.Content>Remember the matching permission rule</Tooltip.Content></Tooltip>}
      <Button ref={allowButton} size="sm" isDisabled={locked} isPending={pending} onPress={() => void respond(true)}>Allow once <CornerDownLeft size={16} /></Button>
    </footer>{feedback}
  </section>;
}

function ApprovalOperation({ workId, toolCallId }: { workId: string; toolCallId: string }) {
  const streamClient = useWorkStreamSource();
  const [open, setOpen] = useState(false);
  const operation = useQuery({
    queryKey: ["approval-operation", workId, toolCallId],
    queryFn: async ({ signal }) => (await streamClient.listMessagesBySession({ agentWorkId: workId, toolCallIds: [toolCallId], kinds: [UnifiedMessageKind.ACTION], limit: 1 }, { signal })).messages[0] ?? null,
    gcTime: 0, retry: false, refetchOnWindowFocus: false,
    refetchInterval: query => query.state.data || query.state.error ? false : 1000,
  });
  const message = operation.data;
  return <>
    {operation.isPending && <p className="sr-only" role="status">Loading operation…</p>}
    {operation.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Could not load the operation.</Alert.Description></Alert.Content><Button size="sm" variant="ghost" onPress={() => void operation.refetch()}>Retry</Button></Alert>}
    {message?.payload.case === "action" && <pre className="wi-command">{message.payload.value.summary}</pre>}
    {message && message.detailFormat !== MessageDetailFormat.UNSPECIFIED && <><Button size="sm" variant="ghost" aria-expanded={open} onPress={() => setOpen(!open)}>View full input</Button>{open && <ToolMessageDetail workId={workId} messageId={message.id} />}</>}
  </>;
}

function WorkQuestion({ request, disabled, pending, respond, feedback }: {
  request: PendingInteraction; disabled: boolean; pending: boolean;
  respond: (allow: boolean, alwaysAllow: boolean, input: string) => Promise<void>; feedback: React.ReactNode;
}) {
  const parsed = parseWorkQuestions(request.questionInputJson);
  const [index, setIndex] = useState(0);
  const [answers, setAnswers] = useState<Record<number, string[]>>({});
  const [custom, setCustom] = useState<Record<number, string>>({});
  const heading = useRef<HTMLHeadingElement>(null);
  const titleId = useId();
  useEffect(() => { heading.current?.focus({ preventScroll: true }); }, [index]);
  if (!parsed) return <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>This question could not be displayed. You can cancel the current turn.</Alert.Description></Alert.Content></Alert>;
  const { input, questions } = parsed;
  const question = questions[index];
  const value = (position: number) => custom[position]?.trim() ? custom[position] : (answers[position] ?? []).join(", ");
  const answer = value(index);
  const finish = (skipped?: number) => {
    const result = Object.fromEntries(questions.flatMap((item, position) => position !== skipped && value(position) ? [[item.question, value(position)]] : []));
    void respond(true, false, JSON.stringify({ ...input, answers: result }));
  };
  const next = (skip: boolean) => {
    if (disabled) return;
    if (index === questions.length - 1) { finish(skip ? index : undefined); return; }
    if (skip) { setAnswers(values => ({ ...values, [index]: [] })); setCustom(values => ({ ...values, [index]: "" })); }
    setIndex(index + 1);
  };
  return <section className="wi-card wi-question" aria-label="Answer questions">
    <header className="wi-heading"><span><MessageCircleQuestion size={18} />Question</span><nav aria-label="Question navigation">
      <Button size="sm" variant="ghost" isIconOnly aria-label="Previous question" isDisabled={disabled || index === 0} onPress={() => setIndex(index - 1)}><ChevronLeft size={18} /></Button>
      <span className="wi-count" aria-live="polite">{index + 1} of {questions.length}</span>
      <Button size="sm" variant="ghost" isIconOnly aria-label="Next question" isDisabled={disabled || index === questions.length - 1} onPress={() => setIndex(index + 1)}><ChevronRight size={18} /></Button>
      <Button size="sm" variant="ghost" isIconOnly aria-label="Skip remaining questions" isDisabled={disabled} onPress={() => finish()}><X size={18} /></Button>
    </nav></header>
    <h3 ref={heading} tabIndex={-1} id={titleId}>{question.question}</h3>
    <div className="wi-choices" role={question.multiSelect ? "group" : "radiogroup"} aria-labelledby={titleId}>{question.options?.map((option, position) => <label className="wi-choice" key={option.label} data-selected={!custom[index] && answers[index]?.includes(option.label)} title={option.description}>
      <input type={question.multiSelect ? "checkbox" : "radio"} name={titleId} value={option.label} disabled={disabled} checked={!custom[index] && Boolean(answers[index]?.includes(option.label))} onChange={() => {
        setAnswers(values => ({ ...values, [index]: question.multiSelect ? values[index]?.includes(option.label) ? values[index].filter(value => value !== option.label) : [...(values[index] ?? []), option.label] : [option.label] }));
        setCustom(values => ({ ...values, [index]: "" }));
      }} /><span className="wi-choice-number" aria-hidden="true">{position + 1}</span><span>{option.label}</span><ArrowRight className="wi-choice-arrow" size={18} aria-hidden="true" />
    </label>)}</div>
    <footer className="wi-question-footer"><label className="wi-custom"><span className="wi-choice-number"><Pencil size={16} /></span><input aria-label="Write your own answer" placeholder="Or write your own answer" disabled={disabled} value={custom[index] ?? ""} onChange={event => { const text = event.target.value; setCustom(values => ({ ...values, [index]: text })); setAnswers(values => ({ ...values, [index]: [] })); }} onKeyDown={event => { if (event.key === "Enter" && !event.nativeEvent.isComposing && event.keyCode !== 229 && answer) { event.preventDefault(); next(false); } }} /></label>
      <Button size="sm" variant="outline" isDisabled={disabled} onPress={() => next(true)}>Skip</Button><Button size="sm" isDisabled={disabled || !answer} isPending={pending} onPress={() => next(false)}>{index === questions.length - 1 ? "Submit" : "Next"}</Button>
    </footer>{feedback}
  </section>;
}
