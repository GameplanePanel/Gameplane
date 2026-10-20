import { Input, Label } from "@heroui/react";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type MessageDescriptor } from "react-intl";

export interface RetentionForm {
  keepLast?: number;
  keepHourly?: number;
  keepDaily?: number;
  keepWeekly?: number;
  keepMonthly?: number;
  keepYearly?: number;
}

interface Props {
  value: RetentionForm;
  onChange: (v: RetentionForm) => void;
}

const RETENTION_LABELS = defineMessages({
  keepLast: { id: "web.components.backups.retention_fields.keep_last", defaultMessage: "Keep last", description: "Retention field: label of the keep-last-N snapshots input" },
  hourly: { id: "web.components.backups.retention_fields.hourly", defaultMessage: "Hourly", description: "Retention field: label of the hourly snapshot count input" },
  daily: { id: "web.components.backups.retention_fields.daily", defaultMessage: "Daily", description: "Retention field: label of the daily snapshot count input" },
  weekly: { id: "web.components.backups.retention_fields.weekly", defaultMessage: "Weekly", description: "Retention field: label of the weekly snapshot count input" },
  monthly: { id: "web.components.backups.retention_fields.monthly", defaultMessage: "Monthly", description: "Retention field: label of the monthly snapshot count input" },
  yearly: { id: "web.components.backups.retention_fields.yearly", defaultMessage: "Yearly", description: "Retention field: label of the yearly snapshot count input" },
});

export function RetentionFields({ value, onChange }: Props) {
  const intl = useOptionalIntl();
  const buckets: Array<{
    key: keyof RetentionForm;
    label: MessageDescriptor;
  }> = [
    { key: "keepLast", label: RETENTION_LABELS.keepLast },
    { key: "keepHourly", label: RETENTION_LABELS.hourly },
    { key: "keepDaily", label: RETENTION_LABELS.daily },
    { key: "keepWeekly", label: RETENTION_LABELS.weekly },
    { key: "keepMonthly", label: RETENTION_LABELS.monthly },
    { key: "keepYearly", label: RETENTION_LABELS.yearly },
  ];

  const handleChange = (key: keyof RetentionForm, input: string) => {
    const numValue = input === "" ? undefined : Number(input);
    onChange({
      ...value,
      [key]: numValue,
    });
  };

  return (
    <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
      {buckets.map(({ key, label }) => (
        <div key={key} className="space-y-1">
          <Label htmlFor={`retention-${key}`} className="text-xs">
            {intl.formatMessage(label)}
          </Label>
          <Input
            id={`retention-${key}`}
            type="number"
            min={0}
            value={value[key] ?? ""}
            onChange={(e) => handleChange(key, e.target.value)}
            placeholder={intl.formatMessage({ id: "web.components.backups.retention_fields.placeholder_zero", defaultMessage: "0", description: "Retention field: placeholder example value of zero" })}
            className="h-9"
          />
        </div>
      ))}
    </div>
  );
}

/**
 * Converts a RetentionForm to the payload shape for the API.
 * Returns an object containing only buckets with a value > 0,
 * or undefined if no buckets have values.
 */
export function buildRetention(v: RetentionForm): RetentionForm | undefined {
  const result: RetentionForm = {};
  let hasAny = false;

  if ((v.keepLast ?? 0) > 0) {
    result.keepLast = v.keepLast;
    hasAny = true;
  }
  if ((v.keepHourly ?? 0) > 0) {
    result.keepHourly = v.keepHourly;
    hasAny = true;
  }
  if ((v.keepDaily ?? 0) > 0) {
    result.keepDaily = v.keepDaily;
    hasAny = true;
  }
  if ((v.keepWeekly ?? 0) > 0) {
    result.keepWeekly = v.keepWeekly;
    hasAny = true;
  }
  if ((v.keepMonthly ?? 0) > 0) {
    result.keepMonthly = v.keepMonthly;
    hasAny = true;
  }
  if ((v.keepYearly ?? 0) > 0) {
    result.keepYearly = v.keepYearly;
    hasAny = true;
  }

  return hasAny ? result : undefined;
}
