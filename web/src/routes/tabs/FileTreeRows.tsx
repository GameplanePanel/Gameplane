import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, File, Folder, Trash2 } from "lucide-react";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { createResourceClient, type FileEntry } from "@/lib/endpoints";
import { resourceKey, type ResourceTarget } from "@/lib/resourceTarget";
import { useOptionalIntl } from "@/lib/i18n/format";
import { cn, formatBytes } from "@/lib/utils";

interface TreeProps {
  target: ResourceTarget;
  checked: FileEntry[];
  active?: string;
  canControl: boolean;
  onOpen: (entry: FileEntry) => void;
  onToggle: (entry: FileEntry) => void;
  onDelete: (entry: FileEntry) => void;
}

export function FileTreeRows({ entries, ...tree }: TreeProps & {entries: FileEntry[]}) {
  return entries.map((entry) => <FileRow key={entry.path} entry={entry} {...tree} />);
}

function FileRow({ entry, ...tree }: TreeProps & {entry: FileEntry}) {
  const intl = useOptionalIntl();
  const [expanded, setExpanded] = useState(false);
  const selected = tree.checked.some((item) => item.path === entry.path);
  const title = expanded
    ? intl.formatMessage({ id: "web.routes.tabs.files.collapse", defaultMessage: "Collapse {path}", description: "Hide a folder's children in the file tree." }, { path: entry.path })
    : intl.formatMessage({ id: "web.routes.tabs.files.expand", defaultMessage: "Expand {path}", description: "Show a folder's children in the file tree." }, { path: entry.path });
  return <li>
    <div className={cn("group flex items-center gap-1 px-2 py-1 text-sm hover:bg-default-100", tree.active === entry.path && "bg-primary/10")}>
      <input type="checkbox" checked={selected} disabled={!selected && tree.checked.length >= 256}
        aria-label={intl.formatMessage({ id: "web.routes.tabs.files.select_path", defaultMessage: "Select {path}", description: "Select a path for file actions." }, {path: entry.path})}
        onChange={() => tree.onToggle(entry)} />
      {entry.dir && <button type="button" aria-label={title} aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
        <ChevronRight className={cn("h-3 w-3", expanded && "rotate-90")} />
      </button>}
      <button type="button" onClick={() => tree.onOpen(entry)} className="flex min-w-0 flex-1 items-center gap-1.5 text-left">
        {entry.dir ? <Folder className="h-3 w-3 shrink-0" /> : <File className="h-3 w-3 shrink-0" />}
        <span className="truncate" title={entry.name}>{entry.name}</span>
      </button>
      {entry.dir ? <button type="button" disabled={!tree.canControl} onClick={() => tree.onDelete(entry)}
        className="p-1 text-danger md:opacity-0 md:group-hover:opacity-100 md:focus-visible:opacity-100"
        aria-label={intl.formatMessage({ id: "web.routes.tabs.files.delete_entry_aria", defaultMessage: "Delete {name}", description: "Files: aria-label of the delete button on a file or folder row; {name} is the file or folder name" }, {name:entry.name})}>
        <Trash2 className="h-3 w-3" />
      </button> : <span className="ml-auto text-xs text-default-500">{formatBytes(entry.size)}</span>}
    </div>
    {expanded && entry.dir && <FolderChildren entry={entry} {...tree} />}
  </li>;
}

function FolderChildren({ entry, ...tree }: TreeProps & {entry: FileEntry}) {
  const { target } = tree;
  const children = useQuery({
    queryKey: resourceKey(target, "files", target.name, entry.path, target.namespace),
    queryFn: ({ signal }) => createResourceClient(target, signal).Files.list(target.name, entry.path, target.namespace),
  });
  return <ul className="ml-3 border-l border-divider">
    {children.error && <li><ErrorBanner err={children.error} /></li>}
    <FileTreeRows entries={children.data ?? []} {...tree} />
  </ul>;
}
