import { Input, TextArea, Button } from "@heroui/react";
import { X } from "lucide-react";
import { useState } from "react";
import { DESCRIPTION_ANNOTATION, type SectionProps } from "./types";
import { Field } from "./Field";
import { useOptionalIntl } from "@/lib/i18n/format";

export function GeneralSection({ draft, onChange, template }: SectionProps) {
  const intl = useOptionalIntl();
  const annotations = draft.metadata.annotations ?? {};
  const labels = draft.metadata.labels ?? {};
  const description = annotations[DESCRIPTION_ANNOTATION] ?? "";
  const [labelDraft, setLabelDraft] = useState({ key: "", value: "" });

  const setAnnotation = (key: string, value: string | undefined) => {
    const next = { ...annotations };
    if (value && value.length > 0) next[key] = value;
    else delete next[key];
    onChange({
      ...draft,
      metadata: {
        ...draft.metadata,
        annotations: Object.keys(next).length ? next : undefined,
      },
    });
  };

  const setLabels = (next: Record<string, string>) => {
    onChange({
      ...draft,
      metadata: {
        ...draft.metadata,
        labels: Object.keys(next).length ? next : undefined,
      },
    });
  };

  const addLabel = () => {
    const key = labelDraft.key.trim();
    const value = labelDraft.value.trim();
    if (!key) return;
    setLabels({ ...labels, [key]: value });
    setLabelDraft({ key: "", value: "" });
  };

  return (
    <div className="space-y-6">
      <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.general.name", defaultMessage: "Name", description: "General: field label for the server name, which is fixed and cannot be edited here" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.general.pinned_to_the_gameserver_uid_cannot", defaultMessage: "Pinned to the GameServer UID; cannot change.", description: "General: hint under the server name field saying the name is pinned to the game server's unique ID and cannot be changed" })}>
        <Input value={draft.metadata.name} disabled />
      </Field>

      <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.general.template", defaultMessage: "Template", description: "General: field label for the game template the server was created from; the field is read-only" })}>
        <Input value={draft.spec.templateRef.name} disabled />
      </Field>

      <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.general.description", defaultMessage: "Description", description: "General: field label for the free-text description of the server" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.general.shown_in_the_dashboard_server_list", defaultMessage: "Shown in the dashboard server list.", description: "General: hint under the description field saying the text is shown in the dashboard server list" })}>
        <TextArea
          value={description}
          onChange={(e) => setAnnotation(DESCRIPTION_ANNOTATION, e.target.value)}
          maxLength={1024}
          placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.general.long_standing_survival_realm_invite_only", defaultMessage: "Long-standing survival realm. Invite-only.", description: "General: example placeholder in the description box before typing; an example text, not a default" })}
        />
      </Field>

      <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.general.image_override", defaultMessage: "Image override", description: "General: field label for an optional container image that replaces the template's image" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.general.pin_a_specific_game_container_image", defaultMessage: "Pin a specific game-container image. Leave blank to use the template default.", description: "General: hint under the image override field; leave it blank to use the template's default image" })}>
        <Input
          value={draft.spec.image ?? ""}
          onChange={(e) =>
            onChange({
              ...draft,
              spec: { ...draft.spec, image: e.target.value || undefined },
            })
          }
          placeholder={template?.spec.image ?? intl.formatMessage({ id: "web.routes.tabs.settings.general.template_image", defaultMessage: "(template image)", description: "General: placeholder of the image override input when the template image is unknown" })}
          spellCheck={false}
        />
      </Field>

      <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.general.labels", defaultMessage: "Labels", description: "General: field label for the list of Kubernetes labels set on the server" })} hint={intl.formatMessage({ id: "web.routes.tabs.settings.general.free_form_kubernetes_labels", defaultMessage: "Free-form Kubernetes labels.", description: "General: hint under the labels field saying labels are free-form Kubernetes labels" })}>
        <div className="space-y-2">
          {Object.entries(labels).map(([k, v]) => (
            <div key={k} className="flex items-center gap-2 rounded border border-border bg-surface/40 px-2 py-1">
              <span className="font-mono text-xs text-muted">{k}</span>
              <span className="text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.general.key_value_separator", defaultMessage: "=", description: "General: separator shown between a label key and its value" })}</span>
              <span className="font-mono text-xs text-fg">{v}</span>
              {/* The tooltip lives on a wrapping element's title (react-aria's
                  Button strips a bare `title` prop) so the button keeps its
                  aria-label as the accessible name for role queries. */}
              <span title={intl.formatMessage({ id: "web.routes.tabs.settings.general.remove_label", defaultMessage: "Remove label", description: "General: tooltip and accessible label of the icon button that removes one label row; keep it short" })} className="ml-auto">
                <Button
                  isIconOnly
                  variant="ghost"
                  size="sm"
                  aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.general.remove_label", defaultMessage: "Remove label", description: "General: tooltip and accessible label of the icon button that removes one label row; keep it short" })}
                  onPress={() => {
                    const next = { ...labels };
                    delete next[k];
                    setLabels(next);
                  }}
                >
                  <X className="h-3 w-3" />
                </Button>
              </span>
            </div>
          ))}
          <div className="flex items-center gap-2">
            <Input
              value={labelDraft.key}
              onChange={(e) => setLabelDraft({ ...labelDraft, key: e.target.value })}
              placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.general.key", defaultMessage: "key", description: "General: placeholder in the label key box of the new label row, shown before typing" })}
              className="flex-1"
            />
            <Input
              value={labelDraft.value}
              onChange={(e) => setLabelDraft({ ...labelDraft, value: e.target.value })}
              placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.general.value", defaultMessage: "value", description: "General: placeholder in the label value box of the new label row, shown before typing" })}
              className="flex-1"
            />
            <Button
              size="sm"
              variant="outline"
              onPress={addLabel}
              isDisabled={!labelDraft.key.trim()}
            >{intl.formatMessage({ id: "web.routes.tabs.settings.general.add", defaultMessage: "Add", description: "Button that adds the typed label key and value" })}</Button>
          </div>
        </div>
      </Field>
    </div>
  );
}

