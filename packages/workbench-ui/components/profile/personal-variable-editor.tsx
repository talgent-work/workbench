import { useState, type FormEvent, type KeyboardEvent } from "react";
import { Alert, Button, Description, FieldError, Input, Label, TextArea, TextField } from "@heroui/react";
import { nameProblem, personalVariableErrorMessage, useSetPersonalVariable } from "../../lib/personal-variable-service";

/** Edits one Personal Variable in place of its row, or a new one after the
 * rows. A saved variable keeps its name: the core saves by name, so a rename
 * would be a second variable. A refused save keeps the draft and shows the
 * core's reason. */
export function PersonalVariableEditor({ initial, kept, onDone }: {
  initial: { name: string; value: string; isNew: boolean }; kept: string[]; onDone: () => void;
}) {
  const save = useSetPersonalVariable();
  const [name, setName] = useState(initial.name);
  const [value, setValue] = useState(initial.value);
  const [touched, setTouched] = useState(false);
  const problem = initial.isNew ? nameProblem(name, kept, true) : "";
  const submit = (event: FormEvent) => {
    event.preventDefault();
    setTouched(true);
    if (problem || save.isPending) return;
    save.mutate({ name, value }, { onSuccess: onDone });
  };
  const escape = (event: KeyboardEvent) => {
    if (event.key !== "Escape" || save.isPending) return;
    event.stopPropagation();
    onDone();
  };
  return (
    <form onSubmit={submit} onKeyDown={escape} aria-label={initial.isNew ? "New variable" : `Edit ${initial.name}`} className="grid gap-3 rounded-lg border border-border p-3">
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
        <TextField value={name} onChange={next => setName(next.trim())} isInvalid={touched && Boolean(problem)} isReadOnly={!initial.isNew} autoFocus={initial.isNew}>
          <Label>Name</Label>
          <Input className="font-mono" placeholder="OPENAI_API_KEY" autoComplete="off" spellCheck={false} />
          <FieldError>{problem}</FieldError>
        </TextField>
        <TextField value={value} onChange={setValue} autoFocus={!initial.isNew}>
          <Label>Value</Label>
          <TextArea rows={value.includes("\n") ? 4 : 1} className="font-mono text-[13px]" autoComplete="off" spellCheck={false} />
          <Description>Multi-line values are kept as they are.</Description>
        </TextField>
      </div>
      {save.isError && (
        <Alert status="danger">
          <Alert.Indicator />
          <Alert.Content><Alert.Title>{personalVariableErrorMessage(save.error, `${name} couldn’t be saved. Try again.`)}</Alert.Title></Alert.Content>
        </Alert>
      )}
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="secondary" isDisabled={save.isPending} onPress={onDone}>Cancel</Button>
        <Button size="sm" type="submit" isPending={save.isPending}>{save.isPending ? "Saving" : "Save"}</Button>
      </div>
    </form>
  );
}
