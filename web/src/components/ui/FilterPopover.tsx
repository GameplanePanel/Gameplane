import type { ReactNode } from "react";
import {
  Button,
  Checkbox,
  Popover,
  Separator,
} from "@heroui/react";
import { cn } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";

interface FilterPopoverProps {
  /**
   * The trigger button content (usually an icon + label).
   */
  children: ReactNode;
  /**
   * List of distinct games to filter by.
   */
  games?: string[];
  /**
   * Currently selected games in the draft filter state.
   */
  selectedGames?: Set<string>;
  /**
   * Callback when a game checkbox is toggled.
   */
  onToggleGame?: (game: string) => void;
  /**
   * List of distinct namespaces to filter by.
   */
  namespaces?: string[];
  /**
   * Currently selected namespaces in the draft filter state.
   */
  selectedNamespaces?: Set<string>;
  /**
   * Callback when a namespace checkbox is toggled.
   */
  onToggleNamespace?: (ns: string) => void;
  /**
   * Callback to apply the current draft filters.
   */
  onApply: () => void;
  /**
   * Callback to clear all draft filters.
   */
  onClear: () => void;
  /**
   * Whether the popover is open.
   */
  isOpen: boolean;
  /**
   * Callback when the popover open state changes.
   */
  onOpenChange: (open: boolean) => void;
  /**
   * Optional className for the popover content.
   */
  contentClassName?: string;
  /** Additional draft filter fields, above the optional game/namespace facets. */
  fields?: ReactNode;
}

/**
 * FilterPopover composes HeroUI Popover with Checkbox controls
 * for filtering server lists by game template and namespace.
 * Follows the design from node FyV6E with Clear/Apply buttons.
 */
export function FilterPopover({
  children,
  games = [],
  selectedGames = new Set(),
  onToggleGame,
  namespaces = [],
  selectedNamespaces = new Set(),
  onToggleNamespace,
  onApply,
  onClear,
  isOpen,
  onOpenChange,
  contentClassName,
  fields,
}: FilterPopoverProps) {
  const intl = useOptionalIntl();
  return (
    <Popover isOpen={isOpen} onOpenChange={onOpenChange}>
      <Popover.Trigger>
        {children}
      </Popover.Trigger>
      <Popover.Content className={cn("w-[280px] max-w-[calc(100vw-2rem)] max-h-[min(36rem,80vh)] overflow-y-auto bg-surface p-1", contentClassName)}>
        <div className="space-y-0">
          {fields && <div className="space-y-3 px-2 py-2">{fields}</div>}
          {fields && (games.length > 0 || namespaces.length > 0) && <Separator className="my-1" />}
          {/* Game section header */}
          {games.length > 0 && (
            <>
              <div className="px-2 py-1.5 text-xs font-semibold text-muted">{intl.formatMessage({ id: "web.components.ui.filter_popover.game", defaultMessage: "Game", description: "Filter popover: small heading above the game choices in the filter menu" })}</div>
              {games.map((game) => (
                <div
                  key={game}
                  className="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-default/40"
                >
                  <Checkbox
                    isSelected={selectedGames.has(game)}
                    onChange={() => onToggleGame?.(game)}
                    className="flex-row items-center gap-2"
                  >
                    <Checkbox.Control>
                      <Checkbox.Indicator />
                    </Checkbox.Control>
                    <Checkbox.Content className="text-sm text-foreground">
                      {game}
                    </Checkbox.Content>
                  </Checkbox>
                </div>
              ))}
            </>
          )}

          {/* Separator between sections */}
          {games.length > 0 && namespaces.length > 0 && (
            <Separator className="my-1" />
          )}

          {/* Namespace section header */}
          {namespaces.length > 0 && (
            <>
              <div className="px-2 py-1.5 text-xs font-semibold text-muted">{intl.formatMessage({ id: "web.components.ui.filter_popover.namespace", defaultMessage: "Namespace", description: "Filter popover: small heading above the namespace choices in the filter menu" })}</div>
              {namespaces.map((ns) => (
                <div
                  key={ns}
                  className="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-default/40"
                >
                  <Checkbox
                    isSelected={selectedNamespaces.has(ns)}
                    onChange={() => onToggleNamespace?.(ns)}
                    className="flex-row items-center gap-2"
                  >
                    <Checkbox.Control>
                      <Checkbox.Indicator />
                    </Checkbox.Control>
                    <Checkbox.Content className="text-sm text-foreground">
                      {ns}
                    </Checkbox.Content>
                  </Checkbox>
                </div>
              ))}
            </>
          )}

          {/* Separator before footer buttons */}
          {(fields || games.length > 0 || namespaces.length > 0) && (
            <Separator className="my-1" />
          )}

          {/* Footer buttons: Clear and Apply */}
          <div className="flex items-center justify-between gap-2 px-2 py-1.5">
            <Button
              size="sm"
              variant="ghost"
              className="h-7"
              onPress={onClear}
            >{intl.formatMessage({ id: "web.components.ui.filter_popover.clear", defaultMessage: "Clear", description: "Filter popover: button that clears all chosen filters; keep it short" })}</Button>
            <Button
              size="sm"
              variant="primary"
              className="h-7"
              onPress={onApply}
            >{intl.formatMessage({ id: "web.components.ui.filter_popover.apply", defaultMessage: "Apply", description: "Filter popover: button that applies the chosen filters; keep it short" })}</Button>
          </div>
        </div>
      </Popover.Content>
    </Popover>
  );
}
