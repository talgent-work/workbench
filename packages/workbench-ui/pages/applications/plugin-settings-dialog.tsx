import { useRef, useState } from "react";
import { Alert, Button, Modal } from "@heroui/react";
import type { InstalledPlugin, PluginSummary } from "@talgent/contracts/marketplace/public/v1/marketplace_pb";
import { ParameterFields } from "../../components/marketplace/parameter-fields";
import { pluginPresentation } from "../../lib/marketplace-plugins";
import { draftsFrom, parseParameters, type ParameterDrafts, type ParameterErrors } from "../../lib/plugin-parameters";
import type { JsonObject } from "@bufbuild/protobuf";

/** A Plugin's settings for the Application: before installing it when it needs
 * some, or for its installation. A blank field that had a saved value clears
 * that setting; Sensitive values are never shown again. */
export function PluginSettingsDialog({ plugin, installed, saving, error, onSave, onClose }: {
  plugin: PluginSummary; installed?: InstalledPlugin; saving: boolean; error: string;
  onSave: (values: JsonObject, cleared: string[]) => void; onClose: () => void;
}) {
  const { name } = pluginPresentation(plugin);
  const [drafts, setDrafts] = useState<ParameterDrafts>({});
  const [errors, setErrors] = useState<ParameterErrors>({});
  const form = useRef<HTMLFormElement>(null);
  const saved = { configuration: installed?.configuration, redacted: installed?.redactedParameters };
  const submit = () => {
    const parsed = parseParameters(plugin.parameters, drafts, saved);
    setErrors(parsed.errors);
    const invalid = Object.keys(parsed.errors)[0];
    if (invalid) { form.current?.querySelector<HTMLElement>(`[name="${CSS.escape(invalid)}"]`)?.focus(); return; }
    const cleared = Object.entries(drafts).filter(([key, value]) => !value.trim() && installed?.configuration?.[key] !== undefined).map(([key]) => key);
    onSave(parsed.values, cleared);
  };
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open) onClose(); }}><Modal.Container placement="center" size="md" scroll="inside"><Modal.Dialog>
    <Modal.Header><Modal.Heading>{installed ? `${name} settings` : `Set up ${name}`}</Modal.Heading></Modal.Header>
    <Modal.Body className="grid gap-3">
      <form ref={form} noValidate id="ap-plugin-settings" onSubmit={event => { event.preventDefault(); submit(); }}>
        <ParameterFields declarations={plugin.parameters} drafts={{ ...draftsFrom(installed?.configuration), ...drafts }} errors={errors} redacted={installed?.redactedParameters}
          onChange={(field, value) => { setDrafts(current => ({ ...current, [field]: value })); setErrors(current => ({ ...current, [field]: "" })); }} />
      </form>
      {error && <Alert status="danger"><Alert.Indicator /><Alert.Content><Alert.Title>{error}</Alert.Title></Alert.Content></Alert>}
    </Modal.Body>
    <Modal.Footer>
      <Button variant="secondary" onPress={onClose}>Cancel</Button>
      <Button type="submit" form="ap-plugin-settings" isPending={saving} isDisabled={Boolean(installed) && !Object.keys(drafts).length}>{installed ? "Save" : "Install"}</Button>
    </Modal.Footer>
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}
