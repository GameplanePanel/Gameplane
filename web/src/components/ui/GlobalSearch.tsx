import { useEffect, useRef, useState, type JSX, type KeyboardEvent } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Search, Server } from "lucide-react";
import {
  SearchFieldRoot,
  SearchFieldInput,
  SearchFieldClearButton,
  PopoverContent,
} from "@heroui/react";
import { cn } from "@/lib/utils";
import { targetKey, targetLabel, targetSearch, useFleetServers, type FleetTarget } from "@/lib/fleet";
import { useOptionalIntl } from "@/lib/i18n/format";

/**
 * GlobalSearch provides a server search dropdown with keyboard navigation.
 * Ported from AppLayout.tsx onto HeroUI's SearchField + Popover primitives.
 * Design export: IdaU7
 */
export function GlobalSearch(): JSX.Element {
  const intl = useOptionalIntl();
  const [q, setQ] = useState("");
  const [open, setOpen] = useState(false);
  const [selectedIndex, setSelectedIndex] = useState(-1);
  const inputRef = useRef<HTMLInputElement>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const navigate = useNavigate();

  useEffect(() => {
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, []);

  const { data, error } = useFleetServers();

  const query = q.trim().toLowerCase();
  const matches =
    query.length > 0
      ? (data?.items ?? [])
          .filter((s) => `${s.target.name} ${s.target.namespace} ${s.target.cluster}`.toLowerCase().includes(query))
          .slice(0, 6)
      : [];

  // Note: selectedIndex is reset to -1 directly in the value-change handler
  // below (and in navigateToServer) whenever the query text changes — not in
  // a useEffect here, since `matches` is a fresh array every render and
  // setState-in-effect off that would cascade renders.

  const navigateToServer = async (target: FleetTarget) => {
    setOpen(false);
    setQ("");
    setSelectedIndex(-1);
    await navigate({ to: "/servers/$name", params: { name: target.name }, search: targetSearch(target) });
  };

  // HeroUI's SearchField already handles Enter (onSubmit, unused here) and
  // Escape (clears the value, which our onChange below turns into a close)
  // as native shortcuts before this handler runs — see
  // node_modules/react-aria/dist/private/searchfield/useSearchField.mjs.
  // We only need Arrow navigation and our own Enter-to-navigate behavior.
  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (!open || matches.length === 0) {
      if (e.key === "Enter") {
        setOpen(true);
      }
      return;
    }

    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        setSelectedIndex((prev) =>
          prev < matches.length - 1 ? prev + 1 : prev
        );
        break;
      case "ArrowUp":
        e.preventDefault();
        setSelectedIndex((prev) => (prev > 0 ? prev - 1 : -1));
        break;
      case "Enter":
        e.preventDefault();
        if (selectedIndex >= 0 && selectedIndex < matches.length) {
          void navigateToServer(matches[selectedIndex].target);
        } else if (matches.length > 0) {
          void navigateToServer(matches[0].target);
        }
        break;
      case "Escape":
        e.preventDefault();
        setOpen(false);
        break;
    }
  };

  return (
    <div className="relative hidden w-[300px] md:block">
      <SearchFieldRoot
        aria-label={intl.formatMessage({ id: "web.components.ui.global_search.search_servers", defaultMessage: "Search servers", description: "Global search: screen-reader name of the server search box, which is hidden on narrow screens; not visible on screen" })}
        value={q}
        onChange={(value) => {
          setQ(value);
          setOpen(value.trim().length > 0);
          setSelectedIndex(-1);
        }}
        className="relative flex items-center rounded-full border border-border bg-surface"
      >
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" />
        <SearchFieldInput
          ref={inputRef}
          placeholder={intl.formatMessage({ id: "web.components.ui.global_search.search_servers_2", defaultMessage: "Search servers…", description: "Global search: example hint inside the server search box telling the user what to type; the ellipsis is part of the text" })}
          data-testid="search-input"
          role="combobox"
          aria-expanded={open}
          aria-controls="global-search-results"
          aria-activedescendant={
            selectedIndex >= 0 && selectedIndex < matches.length
              ? `search-result-${targetKey(matches[selectedIndex].target)}`
              : ""
          }
          onFocus={() => {
            if (query.length > 0) setOpen(true);
          }}
          // Delay so a result click registers before the popover unmounts.
          onBlur={() => {
            if (timerRef.current) clearTimeout(timerRef.current);
            timerRef.current = setTimeout(() => setOpen(false), 120);
          }}
          onKeyDown={handleKeyDown}
          className="h-10 w-full rounded-full border-0 bg-transparent pl-9 pr-8 text-sm text-fg placeholder:text-muted focus:outline-hidden"
        />
        <SearchFieldClearButton className="absolute right-2 top-1/2 -translate-y-1/2" />
      </SearchFieldRoot>

      <PopoverContent
        triggerRef={inputRef}
        isOpen={open && query.length > 0}
        onOpenChange={setOpen}
        isNonModal
        placement="bottom start"
        className="w-72 overflow-hidden rounded-md border border-border bg-background p-0 shadow-lg"
      >
        {data?.partial && <p role="status" className="px-3 py-2 text-xs text-warning">{intl.formatMessage({ id: "web.components.ui.global_search.search_results_are_partial", defaultMessage: "Search results are partial.", description: "Global search: status note at the top of the results list saying some results may be missing" })}</p>}
        <ul role="listbox" id="global-search-results" className="max-h-72 overflow-auto">
          {matches.length === 0 ? (
            <li className="px-3 py-2 text-sm text-muted">
              {error
                ? intl.formatMessage({ id: "web.components.ui.global_search.search_is_unavailable", defaultMessage: "Search is unavailable.", description: "Global search: empty-state text when the search request failed" })
                : data?.partial
                  ? intl.formatMessage({ id: "web.components.ui.global_search.no_matches_in_available_results", defaultMessage: "No matches in available results.", description: "Global search: empty-state text when partial results have no match" })
                  : intl.formatMessage({ id: "web.components.ui.global_search.no_servers_match", defaultMessage: "No servers match.", description: "Global search: empty-state text when no server matches the query" })}
            </li>
          ) : (
            matches.map((server, idx) => (
              <li
                key={targetKey(server.target)}
                id={`search-result-${targetKey(server.target)}`}
                role="option"
                aria-selected={selectedIndex === idx}
                onMouseEnter={() => setSelectedIndex(idx)}
                className={cn(
                  "flex cursor-pointer items-center gap-2 px-3 py-2 text-sm",
                  selectedIndex === idx
                    ? "bg-surface text-fg"
                    : "text-fg hover:bg-surface"
                )}
                data-testid={`search-result-${targetKey(server.target)}`}
              >
                <Server className="h-3.5 w-3.5 shrink-0 text-muted" />
                <a
                  href={`/servers/${encodeURIComponent(server.target.name)}?${new URLSearchParams(targetSearch(server.target))}`}
                  onClick={(event) => { if (!event.ctrlKey && !event.metaKey && !event.shiftKey && event.button === 0) { event.preventDefault(); void navigateToServer(server.target); } }}
                  className="min-w-0 flex-1"
                >
                  <span className="block truncate font-mono">{server.target.name}</span>
                  <span className="block truncate text-xs text-muted">{targetLabel(server.target)}</span>
                </a>
              </li>
            ))
          )}
        </ul>
      </PopoverContent>
    </div>
  );
}
