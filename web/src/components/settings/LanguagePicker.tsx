// Personal Language card on the Theme & Appearance page (spec 019, T044,
// design OD-016). The choice is saved to the signed-in user's account and
// follows them to every browser. "Automatic" saves null, so the app falls back
// to the browser language and then the instance default.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Card, Radio, RadioGroup } from "@heroui/react";
import { Info } from "lucide-react";
import { useMe } from "@/lib/auth";
import { Users } from "@/lib/endpoints";
import { errorTextIntl } from "@/lib/errors";
import { useOptionalIntl } from "@/lib/i18n/format";
import { listedLanguages } from "@/lib/i18n/languages";
import type { User } from "@/types";

// AUTOMATIC is the radio value for "no saved choice" (sent to the API as null).
const AUTOMATIC = "__auto__";

export function LanguagePicker() {
  const intl = useOptionalIntl();
  const queryClient = useQueryClient();
  const { data: me } = useMe();
  const languages = listedLanguages();

  const saved = me?.preferences?.language ?? null;
  // A saved tag that is no longer listed shows as Automatic. The saved value
  // itself is left alone, so it applies again if the language is re-listed.
  const selected = saved !== null && languages.some((l) => l.tag === saved) ? saved : AUTOMATIC;

  const save = useMutation({
    mutationFn: (tag: string | null) => Users.setLanguage(tag),
    onSuccess: (_result, tag) => {
      queryClient.setQueryData<User>(["me"], (old) =>
        old && old.preferences ? { ...old, preferences: { ...old.preferences, language: tag } } : old,
      );
      void queryClient.invalidateQueries({ queryKey: ["me"] });
    },
  });

  const onChange = (value: string) => {
    save.mutate(value === AUTOMATIC ? null : value);
  };

  return (
    <Card className="space-y-4">
      <Card.Header className="space-y-2 pb-2">
        <div className="font-medium text-base">
          {intl.formatMessage({
            id: "web.settings.language.title",
            defaultMessage: "Language",
            description: "Title of the personal Language card on the Theme & Appearance settings page",
          })}
        </div>
        <div className="text-xs text-muted">
          {intl.formatMessage({
            id: "web.settings.language.subtitle",
            defaultMessage:
              "Choose the language of the dashboard. Your choice is saved to your account and follows you to every browser and device.",
            description: "Subtitle under the personal Language card title on the Theme & Appearance settings page",
          })}
        </div>
      </Card.Header>
      <Card.Content className="space-y-4">
        <RadioGroup
          aria-label={intl.formatMessage({
            id: "web.settings.language.group_label",
            defaultMessage: "Language",
            description: "Accessible name of the language radio group on the Theme & Appearance settings page",
          })}
          value={selected}
          onChange={onChange}
          isDisabled={save.isPending}
          className="grid gap-3"
        >
          <Radio value={AUTOMATIC}>
            <Radio.Content className="flex w-full items-center gap-3 rounded-lg border border-border px-4 py-3 transition-colors hover:bg-surface/60 data-[selected]:border-primary data-[selected]:bg-primary/5">
              <Radio.Control className="shrink-0" />
              <span className="min-w-0 flex-1 text-sm font-medium">
                {intl.formatMessage({
                  id: "web.settings.language.automatic",
                  defaultMessage: "Automatic (browser language)",
                  description: "Language option that follows the browser language on the Theme & Appearance settings page",
                })}
              </span>
            </Radio.Content>
          </Radio>
          {languages.map((l) => (
            <Radio key={l.tag} value={l.tag}>
              <Radio.Content className="flex w-full items-center gap-3 rounded-lg border border-border px-4 py-3 transition-colors hover:bg-surface/60 data-[selected]:border-primary data-[selected]:bg-primary/5">
                <Radio.Control className="shrink-0" />
                <span className="min-w-0 flex-1 text-sm font-medium">{l.nativeName}</span>
                <span className="text-xs text-muted">
                  {intl.formatMessage(
                    {
                      id: "web.settings.language.percent_translated",
                      defaultMessage: "{percent} translated",
                      description:
                        "Share of the dashboard text translated into a language, shown beside each language option",
                    },
                    { percent: intl.formatNumber(l.completeness / 100, { style: "percent" }) },
                  )}
                </span>
              </Radio.Content>
            </Radio>
          ))}
        </RadioGroup>
        <p className="flex items-center gap-2 text-xs text-muted">
          <Info className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
          {intl.formatMessage({
            id: "web.settings.language.untranslated_note",
            defaultMessage: "Text that has not been translated yet is shown in English.",
            description: "Note under the language options explaining untranslated text falls back to English",
          })}
        </p>
        {save.isError && (
          <p role="alert" className="text-sm text-danger">
            {errorTextIntl(
              intl,
              save.error,
              intl.formatMessage({
                id: "web.settings.language.save_failed",
                defaultMessage: "Could not save your language.",
                description: "Error shown on the Theme & Appearance settings page when the language cannot be saved",
              }),
            )}
          </p>
        )}
      </Card.Content>
    </Card>
  );
}
