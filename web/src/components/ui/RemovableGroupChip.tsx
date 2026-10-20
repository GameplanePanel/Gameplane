import { Chip } from "@heroui/react";
import { X } from "lucide-react";
import { useOptionalIntl } from "@/lib/i18n/format";

// RemovableGroupChip renders a removable chip for group/role membership display.
// Used in role editors and user management to show assigned groups with inline remove capability.
// Three color variants: secondary (default), orange (warning), violet (accent).

export type RemovableGroupChipVariant = "secondary" | "orange" | "violet";

export interface RemovableGroupChipProps {
  label: string;
  variant?: RemovableGroupChipVariant;
  onRemove: () => void;
  size?: "sm" | "md" | "lg";
  className?: string;
}

const variantMap: Record<RemovableGroupChipVariant, {
  color: "default" | "warning" | "accent";
  variant: "primary" | "secondary" | "soft" | "tertiary";
}> = {
  secondary: {
    color: "default",
    variant: "secondary",
  },
  orange: {
    color: "warning",
    variant: "soft",
  },
  violet: {
    color: "accent",
    variant: "soft",
  },
};

export function RemovableGroupChip({
  label,
  variant = "secondary",
  onRemove,
  size = "sm",
  className,
}: RemovableGroupChipProps) {
  const intl = useOptionalIntl();
  const config = variantMap[variant];

  return (
    <Chip
      color={config.color}
      variant={config.variant}
      size={size}
      data-variant={variant}
      className={className ? `rounded-full ${className}` : "rounded-full"}
    >
      <div className="flex items-center gap-1">
        <span className="text-[10px] font-medium font-mono">{label}</span>
        <button
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          className="inline-flex items-center justify-center hover:opacity-75 transition-opacity"
          aria-label={intl.formatMessage({ id: "web.components.ui.removable_group_chip.remove", defaultMessage: "Remove {label}", description: "Removable chip: accessible name of the remove button; {label} is the chip text, e.g. a group or role name" }, { label })}
          type="button"
        >
          <X size={8} />
        </button>
      </div>
    </Chip>
  );
}
