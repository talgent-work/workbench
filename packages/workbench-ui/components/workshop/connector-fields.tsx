import { ComboBox, Description, FieldError, Input, Label, ListBox, Radio, RadioGroup, Select, TextArea, TextField } from "@heroui/react";
import type { DeclaredProvider } from "@talgent/contracts/marketplace/public/v1/connections_pb";
import { providerHosts, type ConnectorDraft, type DraftProblems } from "../../lib/connector-workshop";

type FieldProps = { draft: ConnectorDraft; set: (patch: Partial<ConnectorDraft>) => void; problems: DraftProblems; touched: boolean };

export function NameField({ draft, set, problems, touched }: FieldProps) {
  return <TextField value={draft.name} onChange={name => set({ name })} isInvalid={touched && Boolean(problems.name)} fullWidth variant="secondary" isRequired>
    <Label>Name</Label><Input placeholder="acme-crm" autoComplete="off" />
    {touched && problems.name ? <FieldError>{problems.name}</FieldError> : <Description>Unique among yours; other people may use the same name. Fixed once added.</Description>}
  </TextField>;
}

export function TransportField({ draft, set }: Pick<FieldProps, "draft" | "set">) {
  return <RadioGroup value={draft.transport} onChange={value => set({ transport: value as ConnectorDraft["transport"], provider: value === "stdio" ? "" : draft.provider })}>
    <Label>How Works reach it</Label>
    <Radio value="http"><Radio.Control><Radio.Indicator /></Radio.Control><Radio.Content><Label>Remote server</Label><Description>An MCP server at an http(s) address.</Description></Radio.Content></Radio>
    <Radio value="stdio"><Radio.Control><Radio.Indicator /></Radio.Control><Radio.Content><Label>Local command</Label><Description>Starts next to the Work, such as a browser tool. Gets no credentials.</Description></Radio.Content></Radio>
  </RadioGroup>;
}

export function ProviderField({ draft, set, problems, providers }: Omit<FieldProps, "touched"> & { providers: DeclaredProvider[] }) {
  const chosen = providers.find(item => item.name === draft.provider);
  return <Select value={draft.provider || "none"} onChange={value => set({ provider: value === "none" ? "" : String(value) })} isDisabled={draft.transport === "stdio"} isInvalid={Boolean(problems.provider)} fullWidth variant="secondary">
    <Label>Signs in with</Label>
    <Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
    <Description>{chosen ? `Each person uses their own ${chosen.displayName} account. Reaches ${providerHosts(chosen).join(", ")}.` : "No sign-in: the server receives no credentials."} Fixed once added.</Description>
    {problems.provider && <FieldError>{problems.provider}</FieldError>}
    <Select.Popover><ListBox>
      <ListBox.Item id="none" textValue="No sign-in"><Label>No sign-in</Label><ListBox.ItemIndicator /></ListBox.Item>
      {providers.map(item => <ListBox.Item key={item.name} id={item.name} textValue={item.displayName}><Label>{item.displayName}</Label><ListBox.ItemIndicator /></ListBox.Item>)}
    </ListBox></Select.Popover>
  </Select>;
}

export function AddressField({ draft, set, problems, touched }: FieldProps) {
  return draft.transport === "http"
    ? <TextField value={draft.url} onChange={url => set({ url })} isInvalid={touched && Boolean(problems.url)} fullWidth variant="secondary" isRequired>
      <Label>Server address</Label><Input placeholder="https://service.corp.example.com/mcp" autoComplete="off" inputMode="url" />
      {touched && problems.url ? <FieldError>{problems.url}</FieldError> : <Description>Changes reach Works started afterwards.</Description>}
    </TextField>
    : <TextField value={draft.command} onChange={command => set({ command })} isInvalid={touched && Boolean(problems.command)} fullWidth variant="secondary" isRequired>
      <Label>Command</Label><Input placeholder="npx chrome-devtools-mcp@0.6.0" autoComplete="off" className="font-mono" spellCheck={false} />
      {touched && problems.command ? <FieldError>{problems.command}</FieldError>
        : <Description>Pin a package version, such as chrome-devtools-mcp@0.6.0 rather than @latest, so every Work runs the same server. If it can’t start in a Work, it just won’t be available there.</Description>}
    </TextField>;
}

export function AboutFields({ draft, set, problems, touched, categories }: FieldProps & { categories: string[] }) {
  return <>
    <TextField value={draft.description} onChange={description => set({ description })} isInvalid={touched && Boolean(problems.description)} fullWidth variant="secondary">
      <Label>Description</Label><TextArea rows={2} placeholder="What a Work can do with it" />
      {touched && problems.description && <FieldError>{problems.description}</FieldError>}
    </TextField>
    <ComboBox allowsCustomValue inputValue={draft.category} onInputChange={category => set({ category })} isInvalid={touched && Boolean(problems.category)} fullWidth variant="secondary">
      <Label>Category</Label>
      <ComboBox.InputGroup><Input placeholder="Choose or type one" /><ComboBox.Trigger /></ComboBox.InputGroup>
      {touched && problems.category ? <FieldError>{problems.category}</FieldError> : <Description>Groups it with the same kind in the Marketplace.</Description>}
      <ComboBox.Popover><ListBox>{categories.map(name => <ListBox.Item key={name} id={name} textValue={name}>{name}<ListBox.ItemIndicator /></ListBox.Item>)}</ListBox></ComboBox.Popover>
    </ComboBox>
  </>;
}
