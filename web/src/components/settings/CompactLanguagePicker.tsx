import { useMemo } from "react";
import { Check, ChevronDown, Globe } from "lucide-react";
import {
  Dropdown,
  DropdownTrigger,
  DropdownPopover,
  DropdownMenu,
  DropdownItem,
} from "@heroui/react";
import { useOptionalIntl } from "@/lib/i18n/format";
import { useLanguage } from "@/lib/i18n/I18nProvider";
import { listedLanguages } from "@/lib/i18n/languages";

// CompactLanguagePicker is the browser-local language switch shown in the
// corner of the unauthenticated Login and Share cards (spec 019, T045). It
// only changes this browser's choice; it never writes to the account, and it
// shows nothing beyond the language names.
export function CompactLanguagePicker() {
  const intl = useOptionalIntl();
  const { language, setLanguage } = useLanguage();
  const options = useMemo(() => listedLanguages(), []);
  const current = options.find((row) => row.tag === language);
  const currentName = current?.nativeName ?? language;

  return (
    <Dropdown>
      <DropdownTrigger
        aria-label={intl.formatMessage({
          id: "web.settings.compact_language_trigger",
          defaultMessage: "Language",
          description:
            "Accessible name of the compact language menu button in the corner of the sign-in and shared-link cards.",
        })}
        className="flex h-8 items-center gap-1.5 rounded-full px-2.5 text-[13px] font-medium text-fg transition-colors cursor-pointer hover:bg-surface"
      >
        <>
          <Globe className="h-3.5 w-3.5 shrink-0 text-muted" aria-hidden="true" />
          <span>{currentName}</span>
          <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted" aria-hidden="true" />
        </>
      </DropdownTrigger>

      <DropdownPopover placement="bottom end">
        <DropdownMenu
          aria-label={intl.formatMessage({
            id: "web.settings.compact_language_menu",
            defaultMessage: "Language options",
            description:
              "Accessible name of the language list opened from the compact language picker on the sign-in and shared-link pages.",
          })}
          className="min-w-[160px]"
        >
          {options.map((option) => (
            <DropdownItem
              key={option.tag}
              id={option.tag}
              textValue={option.nativeName}
              onPress={() => setLanguage(option.tag)}
              className="flex items-center gap-2"
            >
              <span className="flex-1 text-sm">{option.nativeName}</span>
              {option.tag === language && (
                <Check className="h-4 w-4 shrink-0 text-accent" aria-hidden="true" />
              )}
            </DropdownItem>
          ))}
        </DropdownMenu>
      </DropdownPopover>
    </Dropdown>
  );
}
