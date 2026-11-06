import { Alert, Button } from "@heroui/react";
import { useCallback, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { ArrowUp, Square } from "lucide-react";
import { PromptBar } from "../composer/prompt-bar";
import { PromptAddMenu } from "../composer/prompt-bar-controls";
import { SkillHighlight } from "../composer/skill-completion";
import { useSlashCompletion, type SlashCandidate } from "../composer/use-slash-completion";

export interface MessageInputProps {
  workId?: string;
  /** Set while a Turn runs: an empty draft's primary action cancels the Turn,
   * and a written draft is sent into it as Steer. */
  onCancelTurn?: () => Promise<unknown> | unknown;
  context?: ReactNode;
  disabled: boolean;
  sendDisabled?: boolean;
  mode?: "send" | "disabled";
  onSend: (text: string) => Promise<unknown> | unknown;
  onUploadAttachment?: (file: File) => Promise<unknown> | unknown;
  workActionPending?: boolean;
  workActionError?: string;
  placeholder?: string;
  /** Why the composer takes no input while `mode` is "disabled". */
  unavailableText?: string;
  sendLabel?: string;
  inputLabel?: string;
  modelControl?: ReactNode;
  /** Commands and Skills the Harness reported; observed is false before its first report. */
  slash?: { candidates: SlashCandidate[]; observed: boolean };
  /** Regional notices shown above the composer, such as Plugin failures. */
  notices?: ReactNode;
}

export function MessageInput({ onCancelTurn, context, disabled, sendDisabled = false, mode = "send", onSend, onUploadAttachment, workActionPending = false, workActionError = "", placeholder = "Work on anything", unavailableText = "Work is not ready for input…", sendLabel = "Send", inputLabel = "Instruction", modelControl, slash, notices }: MessageInputProps) {
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [sendError, setSendError] = useState("");
  const [uploading, setUploading] = useState(false);
  const [uploadError, setUploadError] = useState("");
  const sendInFlight = useRef(false);
  const cancelInFlight = useRef(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const uploadDisabled = disabled || sending || !onUploadAttachment || uploading;
  const listId = useId();
  const completion = useSlashCompletion(text, setText, slash?.candidates ?? [], `${listId}-commands`);
  // One primary action: with nothing written during a Turn it cancels the Turn.
  const cancelShown = Boolean(onCancelTurn) && !text.trim();
  const primaryDisabled = cancelShown ? disabled || cancelling || workActionPending : disabled || sendDisabled || sending || workActionPending || mode === "disabled" || !text.trim();
  const send = async () => {
    if (primaryDisabled || cancelShown || sendInFlight.current) return;
    const instruction = text.trim();
    if (!instruction) return;
    // The Harness is authoritative: a command it didn't report can't run, so the
    // message is kept for editing instead of being sent.
    const command = slash?.observed ? /^\/([\w:.-]+)(?=\s|$)/.exec(instruction)?.[1] : undefined;
    if (command && !slash!.candidates.some(item => item.name === command)) {
      setSendError(`/${command} isn’t a command or Skill in this Work, so it wasn’t sent. Your message is kept so you can change it.`);
      textarea.current?.focus();
      return;
    }
    sendInFlight.current = true; setSending(true); setSendError("");
    try { await onSend(instruction); setText(""); }
    catch (error) { setSendError(error instanceof Error ? error.message : "Message could not be sent. Try again."); }
    finally { sendInFlight.current = false; setSending(false); }
  };
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (slash && completion.onKeyDown(event)) return;
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) { event.preventDefault(); void send(); }
  };
  const attach = useCallback(() => {
    if (!disabled && !sending && onUploadAttachment && !uploading) fileInput.current?.click();
  }, [disabled, sending, onUploadAttachment, uploading]);
  // "Commands & Skills" starts a "/" token at the end of the draft, which opens the slash menu.
  const openCommands = () => { setText(current => !current || /\s$/.test(current) ? `${current}/` : `${current} /`); setTimeout(() => textarea.current?.focus()); };
  const commandsAvailable = Boolean(slash) && !disabled && !sending && mode !== "disabled";
  const leading = <div className="composer-tool-row">
    <PromptAddMenu disabled={uploadDisabled && !commandsAvailable} attachDisabled={uploadDisabled} onAttach={attach} onCommands={commandsAvailable ? openCommands : undefined} />
    <input ref={fileInput} type="file" className="sr-only" aria-label="Upload attachment file" disabled={uploadDisabled} onChange={async event => {
      const file = event.target.files?.[0]; event.target.value = "";
      if (!file || uploadDisabled || !onUploadAttachment) return;
      setUploading(true); setUploadError("");
      try { await onUploadAttachment(file); }
      catch (error) { setUploadError(error instanceof Error ? error.message : "Attachment upload failed."); }
      finally { setUploading(false); }
    }} />
  </div>;
  return <div className="wpb-conversation">
    {notices}
    {[...new Set([sendError, uploadError, workActionError].filter(Boolean))].map(error => <Alert key={error} status="danger" role="alert" className="mb-2"><Alert.Indicator /><Alert.Content><Alert.Description>{error}</Alert.Description></Alert.Content></Alert>)}
    {slash && completion.menu && <div className="sk-menu-float mb-2">{completion.menu}</div>}
    <PromptBar inline context={<>{slash && <SkillHighlight text={text} candidates={slash.candidates} />}{context}</>} input={{ ref: textarea, value: text, ...(slash ? completion.inputProps : {}), onChange: event => { setText(event.target.value); if (sendError) setSendError(""); }, onKeyDown, disabled: disabled || sending || mode === "disabled", placeholder: mode === "disabled" ? unavailableText : placeholder, "aria-label": inputLabel }} leading={leading}>
      {modelControl}
      <Button size="sm" isIconOnly className="wpb-send" isDisabled={primaryDisabled} aria-label={cancelShown ? "Cancel turn" : mode === "disabled" ? "Work unavailable" : sendLabel} onPress={async () => {
        if (!cancelShown || !onCancelTurn) { void send(); return; }
        if (cancelInFlight.current || primaryDisabled) return;
        cancelInFlight.current = true; setCancelling(true); setSendError("");
        try { await onCancelTurn(); }
        catch (error) { setSendError(error instanceof Error ? error.message : "Could not cancel this turn."); }
        finally { cancelInFlight.current = false; setCancelling(false); }
      }}>{cancelShown ? <Square size={14} fill="currentColor" /> : <ArrowUp size={19} />}</Button>
    </PromptBar>
  </div>;
}
