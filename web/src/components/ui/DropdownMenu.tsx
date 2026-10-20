import { forwardRef, type ReactNode, type ComponentPropsWithRef } from "react";
import {
  Dropdown,
  DropdownTrigger,
  DropdownMenu as HeroDropdownMenu,
  DropdownPopover,
  DropdownItem,
  DropdownSection,
} from "@heroui/react";
import { Separator } from "react-aria-components";
import { useOptionalIntl } from "@/lib/i18n/format";

// Root wrapper for dropdown — uses HeroUI Dropdown (which is MenuTriggerPrimitive-based)
export function DropdownMenu({ children }: { children: ReactNode }) {
  return <Dropdown>{children}</Dropdown>;
}

// Trigger — passes through to HeroUI's DropdownTrigger
export const DropdownMenuTrigger = DropdownTrigger;

// Content wrapper — renders HeroUI's DropdownMenu with semantic styling
export function DropdownMenuContent({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <DropdownPopover>
      <HeroDropdownMenu className={`min-w-56 ${className ?? ""}`}>
        {children}
      </HeroDropdownMenu>
    </DropdownPopover>
  );
}

// Separator — divider between groups of items using react-aria Separator
export function DropdownMenuSeparator() {
  return <Separator className="my-1 bg-border" />;
}

// Item — individual menu item with icon, label, and optional destructive/disabled states
export function DropdownMenuItem({
  icon,
  label,
  onSelect,
  destructive,
  disabled,
  hint,
}: {
  icon: ReactNode;
  label: string;
  onSelect: () => void;
  destructive?: boolean;
  disabled?: boolean;
  hint?: string;
}) {
  const intl = useOptionalIntl();
  return (
    <DropdownItem
      key={label}
      onAction={onSelect}
      isDisabled={disabled}
      className={`flex gap-2 ${
        destructive ? "text-danger" : "text-foreground"
      } ${disabled ? "opacity-50" : ""}`}
      aria-label={hint ? intl.formatMessage({ id: "web.components.ui.dropdown_menu.label_with_hint", defaultMessage: "{label}: {hint}", description: "Dropdown menu item: accessible name combining the item label and its hint; {label} is the item text and {hint} the hint text" }, { label, hint }) : label}
    >
      <div className="flex items-center gap-2 w-full" title={hint}>
        {icon}
        <span>{label}</span>
      </div>
    </DropdownItem>
  );
}

// CheckboxItem — menu item with checkbox indicator
type DropdownMenuCheckboxItemProps = Omit<
  ComponentPropsWithRef<typeof DropdownItem>,
  "children" | "checked" | "className"
> & {
  checked?: boolean;
  children?: ReactNode;
  className?: string;
};

export const DropdownMenuCheckboxItem = forwardRef<
  HTMLDivElement,
  DropdownMenuCheckboxItemProps
>(({ checked, children, className, ...props }, ref) => {
  const intl = useOptionalIntl();
  return (
    <DropdownItem
      ref={ref}
      {...props}
      className={`flex gap-2 text-foreground ${className ?? ""}`}
    >
      <div className="flex items-center gap-2 w-full">
        {checked && (
          <span className="flex items-center justify-center w-4 h-4">
            {intl.formatMessage({ id: "web.components.ui.dropdown_menu.checked_mark", defaultMessage: "✓", description: "Check mark shown beside a checked menu item" })}
          </span>
        )}
        {!checked && <span className="w-4 h-4" />}
        <span>{children}</span>
      </div>
    </DropdownItem>
  );
});
DropdownMenuCheckboxItem.displayName = "DropdownMenuCheckboxItem";

// Section — group items with optional title
export const DropdownMenuSection = DropdownSection;
