import { toJson } from "@bufbuild/protobuf";
import { ValueSchema } from "@bufbuild/protobuf/wkt";
import { Description, FieldError, Input, Label, ListBox, Select, Switch, TextArea, TextField } from "@heroui/react";
import type { ParameterDeclaration } from "@talgent/workbench-contracts/marketplace/public/v1/marketplace_pb";
import { hasDefault, parameterLabel, type ParameterDrafts, type ParameterErrors } from "../../lib/plugin-parameters";

function defaultText(declaration: ParameterDeclaration) {
  if (declaration.sensitive || !declaration.default) return "";
  const value = toJson(ValueSchema, declaration.default);
  return Array.isArray(value) ? value.join(", ") : String(value);
}

// Fields follow the published declarations. Sensitive values are never echoed:
// a saved one shows only that it exists and can be replaced.
export function ParameterFields({ declarations, drafts, errors = {}, redacted = [], inherited, onChange }: {
  declarations: Record<string, ParameterDeclaration>; drafts: ParameterDrafts; errors?: ParameterErrors; redacted?: string[];
  /** Personal values a Work override starts from, shown as a hint. */
  inherited?: ParameterDrafts; onChange: (name: string, value: string) => void;
}) {
  return <div className="mk-form">{Object.entries(declarations).map(([name, declaration]) => {
    const fallback = defaultText(declaration);
    const hint = inherited?.[name] !== undefined ? `Your default: ${inherited[name]}.` : fallback ? `Default: ${fallback}.` : "";
    const optional = !declaration.required || hasDefault(declaration);
    const label = <Label>{parameterLabel(name, declaration)}{optional && <span className="mk-hint"> (optional)</span>}</Label>;
    const description = [declaration.description, hint, declaration.multiple ? "One value per line." : "", declaration.sensitive ? "Stored securely and never shown again." : ""].filter(Boolean).join(" ");
    if (declaration.type === "boolean" && !declaration.multiple) {
      const selected = (drafts[name] ?? inherited?.[name] ?? fallback) === "true";
      return <Switch key={name} isSelected={selected} onChange={value => onChange(name, String(value))}>
        <Switch.Control><Switch.Thumb /></Switch.Control><Switch.Content>{label}<Description>{description}</Description></Switch.Content>
      </Switch>;
    }
    if (declaration.options.length && !declaration.multiple) {
      return <Select key={name} name={name} value={drafts[name] || null} isInvalid={Boolean(errors[name])} onChange={value => onChange(name, String(value ?? ""))} placeholder="Choose an option" fullWidth variant="secondary">
        {label}<Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger><Description>{description}</Description>{errors[name] && <FieldError>{errors[name]}</FieldError>}
        <Select.Popover><ListBox>{declaration.options.map(option => <ListBox.Item key={option} id={option} textValue={option}><Label>{option}</Label><ListBox.ItemIndicator /></ListBox.Item>)}</ListBox></Select.Popover>
      </Select>;
    }
    const saved = declaration.sensitive && redacted.includes(name);
    return <TextField key={name} name={name} value={drafts[name] ?? ""} onChange={value => onChange(name, value)} isInvalid={Boolean(errors[name])} type={declaration.sensitive ? "password" : "text"} fullWidth variant="secondary">
      {label}
      {declaration.multiple ? <TextArea rows={3} placeholder={fallback} /> : <Input autoComplete="off" inputMode={declaration.type === "number" ? "decimal" : undefined} placeholder={saved ? "Saved — enter a new value to replace it" : fallback} />}
      <Description>{description}</Description>
      {errors[name] && <FieldError>{errors[name]}</FieldError>}
    </TextField>;
  })}</div>;
}
