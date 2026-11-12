import { useResourceAccess, useResourceTarget, resourceKey, type ResourceTarget } from "@/lib/resourceTarget";
import {
  useEffect,
  useId,
  useMemo,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type ChangeEvent,
  type ReactNode,
} from "react";
import { skipToken, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Modal,
  ModalBackdrop,
  ModalContainer,
  ModalDialog,
  ModalHeader,
  ModalBody,
  ModalFooter,
  Button,
  Input,
} from "@heroui/react";
import Editor, { startFileLanguageService } from "@/lib/monaco-editor";
import {
  ChevronLeft,
  Circle,
  X,
  Download,
  File as FileIcon,
  FilePlus,
  Folder,
  FolderPlus,
  Loader2,
  RefreshCw,
  Save,
  Trash2,
  Upload,
} from "lucide-react";

import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { createResourceClient, type FileEntry } from "@/lib/endpoints";
import { cn } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";
import { FileDrafts } from "@/lib/fileDrafts";
import { FileTreeRows } from "./FileTreeRows";

const ROOT = "/";

interface FileLocation {
  path?: string;
  onNavigate?: (path: string) => void;
}

export function FilesTab({ name, ns, ...location }: { name: string; ns?: string } & FileLocation) {
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  // A different server incarnation, namespace or cluster starts a fresh browser.
  // Old asynchronous work remains attached to the instance that initiated it.
  return <FilesBrowser key={JSON.stringify(resourceKey(resourceTarget, "file-editor"))} resourceTarget={resourceTarget} {...location} />;
}

function FilesBrowser({ resourceTarget, path, onNavigate }: { resourceTarget: ResourceTarget } & FileLocation) {
  const intl = useOptionalIntl();
  const canControl = useResourceAccess()?.canControl === true;
  const { name, namespace: ns } = resourceTarget;
  const resourceClient = useMemo(() => createResourceClient(resourceTarget), [resourceTarget]);
  const { Files } = resourceClient;
  const qc = useQueryClient();
  const listKey = (cwd: string) => resourceKey(resourceTarget, "files", name, cwd, ns);
  const editorId = useId();

  const [localPath, setLocalPath] = useState(ROOT);
  const location = path ?? localPath;
  const isFolder = location.endsWith("/");
  const cwd = isFolder ? location.replace(/\/$/, "") || ROOT : parentOf(location);
  const selectedPath = isFolder ? undefined : location;
  const selected = selectedPath ? { path: selectedPath, name: selectedPath.slice(selectedPath.lastIndexOf("/") + 1), dir: false } : null;
  const { data: cachedDrafts } = useQuery<FileDrafts>({
    queryKey: resourceKey(resourceTarget, "editor-drafts"),
    queryFn: skipToken,
    initialData: () => new FileDrafts(async (file, body) => {
      await Files.write(name, file, body, ns);
      void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "files", name, parentOf(file), ns) });
    }),
    gcTime: Infinity,
    staleTime: Infinity,
  });
  const drafts = cachedDrafts!; // Synchronous initialData, never fetched or undefined.
  useSyncExternalStore(drafts.subscribe, drafts.snapshot);
  useEffect(() => {
    drafts.setEnabled(canControl);
    return () => drafts.setEnabled(false);
  }, [drafts, canControl]);
  const mounted = useRef(true);
  useLayoutEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  const currentLocation = useRef(location);
  useLayoutEffect(() => { currentLocation.current = location; }, [location]);
  const loadedFile = selectedPath ? drafts.get(selectedPath) : undefined;
  const serverContent = loadedFile?.saved ?? null;
  const editorValue = loadedFile?.text ?? "";
  const [loadError, setLoadError] = useState<{ path: string; message: string } | null>(null);
  const [opError, setOpError] = useState<string | null>(null);

  const [confirmDelete, setConfirmDelete] = useState<FileEntry[] | null>(null);
  const [checked, setChecked] = useState<FileEntry[]>([]);
  const [zipOpen, setZipOpen] = useState(false);
  const [mkdirOpen, setMkdirOpen] = useState(false);
  const [newFileOpen, setNewFileOpen] = useState(false);

  const uploadInputRef = useRef<HTMLInputElement | null>(null);

  const dirty = serverContent !== null && editorValue !== serverContent;

  const { data: entries, error: listError, isFetching, refetch } = useQuery({
    queryKey: listKey(cwd),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Files.list(name, cwd, ns),
  });

  // Resolve the path itself. A large directory can have a truncated listing.
  useEffect(() => {
    if (!selectedPath || drafts.get(selectedPath)) return;
    const controller = new AbortController();
    const release = drafts.trackRead(selectedPath, controller);
    createResourceClient(resourceTarget, controller.signal).Files.read(name, selectedPath, ns)
      .then((text) => {
        if (!controller.signal.aborted) drafts.open(selectedPath, text);
      })
      .catch((err: Error) => {
        if (!controller.signal.aborted) setLoadError({ path: selectedPath, message: err.message });
      }).finally(release);
    return () => { controller.abort(); release(); };
  }, [selectedPath, name, ns, resourceTarget, drafts]);

  function changeLocation(next: string) {
    if (!mounted.current) return;
    currentLocation.current = next;
    setLoadError(null);
    setOpError(null);
    setLocalPath(next);
    onNavigate?.(next);
  }

  function selectFile(entry: Pick<FileEntry, "path">) {
    changeLocation(entry.path);
  }

  function navigateTo(folder: string) {
    changeLocation(folder === ROOT ? ROOT : folder + "/");
  }

  function toggleEntry(entry: FileEntry) {
    setChecked((current) => current.some((item) => item.path === entry.path)
      ? current.filter((item) => item.path !== entry.path) : [...current, entry]);
  }

  const selectedRoots = checked.filter((item) => !checked.some((other) => other.dir && item.path.startsWith(other.path + "/")));
  const deleteMutation = useMutation({
    mutationFn: async (items: FileEntry[]) => {
      const removed: string[] = [];
      const errors: string[] = [];
      for (const entry of items) {
        try {
          await drafts.remove(entry.path, () => Files.remove(name, entry.path, entry.dir, ns));
          removed.push(entry.path);
        } catch (err) {
          errors.push(entry.path + " — " + (err as Error).message);
        }
      }
      return { removed, errors, total: items.length };
    },
    onSuccess: ({ removed, errors, total }) => {
      const deleted = (p: string) => removed.some((root) => p === root || p.startsWith(root + "/"));
      const current = currentLocation.current.replace(/\/$/, "");
      const root = removed.find((entry) => current === entry || current.startsWith(entry + "/"));
      if (root) navigateTo(parentOf(root));
      setChecked((items) => items.filter((entry) => !deleted(entry.path)));
      setConfirmDelete(null);
      if (errors.length) setOpError(intl.formatMessage({ id: "web.routes.tabs.files.partial_delete", defaultMessage: "Deleted {count} of {total}. {errors}", description: "Reports how many selected paths were deleted and which paths failed." }, {count: removed.length, total, errors: errors.join("; ")}));
      void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "files") });
      void qc.invalidateQueries({ queryKey: ["fleet"] });
    },
  });

  const archiveMutation = useMutation({
    mutationFn: async (dest?: string) => {
      const paths = selectedRoots.map((entry) => entry.path);
      const blob = await Files.archive(name, paths, dest && joinPath(cwd, dest), ns);
      if (blob) {
        const url = URL.createObjectURL(blob);
        const link = document.createElement("a");
        link.href = url;
        link.download = "selection.zip";
        link.click();
        setTimeout(() => URL.revokeObjectURL(url), 60_000);
      }
    },
    onSuccess: () => {
      setZipOpen(false);
      setOpError(null);
      void qc.invalidateQueries({ queryKey: listKey(cwd) });
    },
    onError: (err: Error) => setOpError(err.message),
  });

  const mkdirMutation = useMutation({
    mutationFn: (folderName: string) =>
      Files.mkdir(name, joinPath(cwd, folderName), ns),
    onSuccess: async () => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setMkdirOpen(false);
      await qc.invalidateQueries({ queryKey: listKey(cwd) });
      setOpError(null);
    },
    onError: (err: Error) => setOpError(err.message),
  });

  const newFileMutation = useMutation({
    mutationFn: async (fileName: string) => {
      const origin = currentLocation.current;
      const path = joinPath(cwd, fileName);
      await Files.create(name, path, "", ns);
      return { path, origin };
    },
    onSuccess: async ({ path, origin }) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setNewFileOpen(false);
      await qc.invalidateQueries({ queryKey: listKey(cwd) });
      if (currentLocation.current === origin) selectFile({ path });
      setOpError(null);
    },
    onError: (err: Error) => setOpError(err.message),
  });

  const uploadMutation = useMutation({
    mutationFn: (files: FileList) => Files.upload(name, cwd, files, ns),
    onSuccess: async () => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      await qc.invalidateQueries({ queryKey: listKey(cwd) });
      setOpError(null);
    },
    onError: (err: Error) => setOpError(err.message),
  });

  function onUploadPicked(e: ChangeEvent<HTMLInputElement>) {
    const files = e.target.files;
    if (canControl && files && files.length > 0) uploadMutation.mutate(files);
    e.target.value = "";
  }

  function downloadSelected() {
    if (!selected || selected.dir) return;
    window.location.href = Files.downloadURL(name, selected.path, ns);
  }

  return (
    <div className="flex h-full flex-col gap-3">
      <div className="flex items-center justify-between gap-3">
        <BreadcrumbsNav cwd={cwd} onNavigate={navigateTo} />
        <div className="flex items-center gap-2">
          <Button
            variant="secondary"
            size="sm"
            isDisabled={!canControl} onPress={() => setNewFileOpen(true)}
          >
            <FilePlus className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.tabs.files.new_file", defaultMessage: "New file", description: "Files tab: button that opens the prompt to create a new file, and the title of that prompt dialog; keep it short" })}</Button>
          <Button
            variant="secondary"
            size="sm"
            isDisabled={!canControl} onPress={() => setMkdirOpen(true)}
          >
            <FolderPlus className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.tabs.files.new_folder", defaultMessage: "New folder", description: "Files tab: button that opens the prompt to create a new folder, and the title of that prompt dialog; keep it short" })}</Button>
          <Button
            variant="secondary"
            size="sm"
            onPress={() => uploadInputRef.current?.click()}
            isDisabled={!canControl || uploadMutation.isPending}
          >
            {uploadMutation.isPending ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Upload className="h-4 w-4" />
            )} {intl.formatMessage({ id: "web.routes.tabs.files.upload", defaultMessage: "Upload", description: "Files tab: button that uploads a file to the current folder; shows a spinner while the upload runs; keep it short" })}</Button>
          <input
            ref={uploadInputRef}
            type="file"
            multiple
            className="hidden"
            onChange={onUploadPicked}
            data-testid="files-upload-input"
          />
          <Button
            isIconOnly
            variant="ghost"
            size="sm"
            aria-label={intl.formatMessage({ id: "web.routes.tabs.files.refresh", defaultMessage: "Refresh", description: "Files tab: icon-only button that reloads the file list; read by screen readers; keep it short" })}
            onPress={() => refetch()}
          >
            <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
          </Button>
        </div>
      </div>

      {opError && <ErrorBanner err={opError} onDismiss={() => setOpError(null)} />}
      {loadedFile?.error && <ErrorBanner err={loadedFile.error} />}
      {listError && <ErrorBanner err={listError} />}
      {checked.length > 0 && (
        <div className="flex flex-wrap items-center gap-2">
          <span>{intl.formatMessage({ id: "web.routes.tabs.files.selected_count", defaultMessage: "{count} selected", description: "Number of checked file paths." }, { count: checked.length })}</span>
          <Button size="sm" isDisabled={archiveMutation.isPending} onPress={() => archiveMutation.mutate(undefined)}>{intl.formatMessage({ id: "web.routes.tabs.files.download_zip", defaultMessage: "Download ZIP", description: "Download the selected paths as one ZIP archive." })}</Button>
          <Button size="sm" isDisabled={!canControl || archiveMutation.isPending} onPress={() => setZipOpen(true)}>{intl.formatMessage({ id: "web.routes.tabs.files.create_zip", defaultMessage: "Create ZIP", description: "Create a ZIP of the selected files on the server." })}</Button>
          <Button size="sm" variant="danger" isDisabled={!canControl || deleteMutation.isPending} onPress={() => setConfirmDelete(selectedRoots)}>{intl.formatMessage({ id: "web.routes.tabs.files.delete_selected", defaultMessage: "Delete selected", description: "Delete all selected files and folders." })}</Button>
        </div>
      )}

      {drafts.paths().length > 0 && <nav aria-label={intl.formatMessage({ id: "web.routes.tabs.files.open_files", defaultMessage: "Open files", description: "Accessible name for the open file tabs." })} className="flex shrink-0 gap-1 overflow-x-auto border-b border-divider">
        {drafts.paths().map((p) => <div key={p} className="flex items-center">
          <button type="button" aria-current={p === selectedPath ? "page" : undefined} className="px-2 py-1 text-xs aria-current:bg-default-100" onClick={() => selectFile({path: p})}>{p}{drafts.get(p)?.text !== drafts.get(p)?.saved && <Circle aria-hidden="true" className="ml-1 inline size-2 fill-current" />}</button>
          <button type="button" className="pr-2" aria-label={intl.formatMessage({ id: "web.routes.tabs.files.close_file", defaultMessage: "Close {path}", description: "Close a file tab after saving its edits." }, {path: p})} onClick={async () => {
            let closed = await drafts.close(p);
            if (!closed && drafts.get(p)?.error && window.confirm(intl.formatMessage({
              id: "web.routes.tabs.files.discard_unsaved_changes", defaultMessage: "You have unsaved changes. Discard them and continue?", description: "Files: confirm prompt shown before discarding unsaved file edits",
            }))) closed = drafts.discard(p);
            if (closed && currentLocation.current === p) navigateTo(parentOf(p));
          }}><X aria-hidden="true" className="size-3" /></button>
        </div>)}
      </nav>}
      <div className="flex min-h-0 flex-1 overflow-hidden rounded-lg border border-divider bg-surface">
        <aside
          className={cn(
            "w-full shrink-0 flex-col border-r border-divider md:flex md:w-72",
            isFolder ? "flex" : "hidden",
          )}
        >
          <div className="border-b border-divider bg-background px-3 py-2 font-mono text-xs text-default-500">
            {cwd}
          </div>
          <ul className="flex-1 overflow-auto">
            {cwd !== ROOT && (
              <li
                className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-sm text-default-500 hover:bg-default-100"
                onClick={() => navigateTo(parentOf(cwd))}
              >
                <Folder className="h-3 w-3" /> {intl.formatMessage({ id: "web.routes.tabs.files.parent_directory", defaultMessage: "..", description: "Files: row that opens the parent folder of the current folder" })}
              </li>
            )}
            {entries?.length === 0 && (
              <li className="px-3 py-4 text-center text-xs text-default-500">{intl.formatMessage({ id: "web.routes.tabs.files.empty_folder", defaultMessage: "Empty folder", description: "Files tab: text shown in the file list when the current folder has no entries" })}</li>
            )}
            <FileTreeRows entries={entries ?? []} target={resourceTarget} checked={checked} active={selectedPath}
              canControl={canControl} onToggle={toggleEntry} onOpen={(entry) => entry.dir ? navigateTo(entry.path) : selectFile(entry)}
              onDelete={(entry) => setConfirmDelete([entry])} />
          </ul>
        </aside>

        <main
          className={cn(
            "min-w-0 flex-1 flex-col md:flex",
            isFolder ? "hidden" : "flex",
          )}
        >
          <div className="flex items-center border-b border-divider bg-background px-2 py-1.5 md:hidden">
            <Button
              isIconOnly
              variant="ghost"
              size="sm"
              onPress={() => navigateTo(cwd)}
              aria-label={intl.formatMessage({ id: "web.routes.tabs.files.back_to_files", defaultMessage: "Back to files", description: "Files tab: icon-only button that returns from a file view to the file tree; read by screen readers" })}
              className="text-default-500"
            >
              <ChevronLeft className="h-4 w-4" />
            </Button>
            <span className="text-xs text-default-500 ml-1">{intl.formatMessage({ id: "web.routes.tabs.files.files", defaultMessage: "Files", description: "Files tab: label beside the back button in the file pane header, naming the files area" })}</span>
          </div>
          {selected && !selected.dir ? (
            <>
              <div className="flex items-center justify-between border-b border-divider bg-background px-4 py-2">
                <div className="flex items-center gap-2 text-sm">
                  <FileIcon className="h-4 w-4 text-primary" />
                  <span className="font-medium text-foreground">{selected.name}</span>
                  {dirty && (
                    <span className="text-xs text-warning">{intl.formatMessage({ id: "web.routes.tabs.files.modified", defaultMessage: "· modified", description: "Files tab: warning marker shown beside the open file name when it has unsaved edits" })}</span>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="secondary"
                    size="sm"
                    onPress={downloadSelected}
                  >
                    <Download className="h-3 w-3" /> {intl.formatMessage({ id: "web.routes.tabs.files.download", defaultMessage: "Download", description: "Files tab: button that downloads the selected file to the user's computer; keep it short" })}</Button>
                  <Button
                    variant="danger"
                    size="sm"
                    isDisabled={!canControl} onPress={() => setConfirmDelete([{...selected, size: 0}])}
                  >
                    <Trash2 className="h-3 w-3" /> {intl.formatMessage({ id: "web.routes.tabs.files.delete", defaultMessage: "Delete", description: "Files tab: red button that deletes the selected file or folder, and the confirm button of the delete dialog; keep it short" })}</Button>
                  <Button
                    size="sm"
                    isDisabled={!canControl || !dirty || !!loadedFile?.pending || loadedFile?.deleting}
                    onPress={() => { if (canControl) void drafts.save(selected.path); }}
                    variant="primary"
                  >
                    {loadedFile?.pending ? (
                      <Loader2 className="h-3 w-3 animate-spin" />
                    ) : (
                      <Save className="h-3 w-3" />
                    )} {intl.formatMessage({ id: "web.routes.tabs.files.save", defaultMessage: "Save", description: "Files tab: button that saves edits to the open file; shows a spinner while saving; keep it short" })}</Button>
                </div>
              </div>
              <div className="min-h-0 flex-1 bg-background">
                {loadError && loadError.path === selectedPath ? (
                  <div className="grid h-full place-items-center text-sm text-danger">
                    {loadError.message}
                  </div>
                ) : serverContent === null ? (
                  <div className="grid h-full place-items-center text-sm text-default-500">
                    <Loader2 className="h-4 w-4 animate-spin" />
                  </div>
                ) : (
                  <Editor
                    key={selected.path}
                    theme="vs-dark"
                    language={guessLang(selected.name)}
                    path={`file:///workspace/${encodeURIComponent(selected.name)}?editor=${encodeURIComponent(editorId)}&path=${encodeURIComponent(selected.path)}`}
                    saveViewState={false}
                    onMount={startFileLanguageService}
                    value={editorValue}
                    onChange={(v) => { if (canControl) drafts.edit(selected.path, v ?? ""); }}
                    options={{
                      readOnly: !canControl || loadedFile?.deleting,
                      minimap: { enabled: false },
                      fontFamily: "JetBrains Mono",
                    }}
                  />
                )}
              </div>
            </>
          ) : (
            <div className="grid h-full place-items-center text-sm text-default-500">{intl.formatMessage({ id: "web.routes.tabs.files.select_a_file_to_edit", defaultMessage: "Select a file to edit.", description: "Files tab: empty-state text in the editor pane shown when no file is selected" })}</div>
          )}
        </main>
      </div>

      {confirmDelete && (
        <ConfirmDialog
          open={!!confirmDelete}
          onOpenChange={(open) => !open && setConfirmDelete(null)}
          title={confirmDelete.length > 1 ? intl.formatMessage({ id: "web.routes.tabs.files.delete_many_title", defaultMessage: "Delete {count} items?", description: "Confirmation to delete multiple selected paths." }, { count: confirmDelete.length }) : intl.formatMessage({ id: "web.routes.tabs.files.delete_entry_title", defaultMessage: "Delete {name}?", description: "Files: title of the confirm dialog for deleting a file or folder; {name} is the file or folder name" }, { name: confirmDelete[0].name })}
          description={
            confirmDelete.some((entry) => entry.dir) || confirmDelete.length > 1
              ? intl.formatMessage({ id: "web.routes.tabs.files.delete_folder_body", defaultMessage: "This permanently removes the folder and everything inside it from the server's data volume. This action cannot be undone.", description: "Files: body of the confirm dialog for deleting a folder" })
              : intl.formatMessage({ id: "web.routes.tabs.files.delete_file_body", defaultMessage: "This permanently removes the file from the server's data volume. This action cannot be undone.", description: "Files: body of the confirm dialog for deleting a file" })
          }
          destructive
          confirmLabel={intl.formatMessage({ id: "web.routes.tabs.files.delete", defaultMessage: "Delete", description: "Files tab: red button that deletes the selected file or folder, and the confirm button of the delete dialog; keep it short" })}
          busy={deleteMutation.isPending}
          onConfirm={() => deleteMutation.mutate(confirmDelete)}
        />
      )}

      <NamePromptDialog open={zipOpen} onOpenChange={setZipOpen}
        title={intl.formatMessage({ id: "web.routes.tabs.files.create_zip", defaultMessage: "Create ZIP", description: "Create a ZIP of the selected files on the server." })}
        label={intl.formatMessage({ id: "web.routes.tabs.files.zip_name", defaultMessage: "Archive filename (.zip)", description: "Label for the name of a ZIP to save on the server." })}
        confirmLabel={intl.formatMessage({ id: "web.routes.tabs.files.create", defaultMessage: "Create", description: "Files tab: confirm button of the new file and new folder prompts that creates the item; keep it short" })}
        busy={archiveMutation.isPending} onSubmit={(value) => archiveMutation.mutate(value)} />
      <NamePromptDialog
        open={mkdirOpen}
        onOpenChange={setMkdirOpen}
        title={intl.formatMessage({ id: "web.routes.tabs.files.new_folder", defaultMessage: "New folder", description: "Files tab: button that opens the prompt to create a new folder, and the title of that prompt dialog; keep it short" })}
        label={intl.formatMessage({ id: "web.routes.tabs.files.folder_name", defaultMessage: "Folder name", description: "Files tab: field label for the name of a new folder in the new folder prompt" })}
        placeholder={intl.formatMessage({ id: "web.routes.tabs.files.my_folder", defaultMessage: "my-folder", description: "Files tab: example placeholder in the new folder name field; an example name, not a default" })}
        confirmLabel={intl.formatMessage({ id: "web.routes.tabs.files.create", defaultMessage: "Create", description: "Files tab: confirm button of the new file and new folder prompts that creates the item; keep it short" })}
        busy={mkdirMutation.isPending}
        onSubmit={(value) => mkdirMutation.mutate(value)}
      />
      <NamePromptDialog
        open={newFileOpen}
        onOpenChange={setNewFileOpen}
        title={intl.formatMessage({ id: "web.routes.tabs.files.new_file", defaultMessage: "New file", description: "Files tab: button that opens the prompt to create a new file, and the title of that prompt dialog; keep it short" })}
        label={intl.formatMessage({ id: "web.routes.tabs.files.file_name", defaultMessage: "File name", description: "Files tab: field label for the name of a new file in the new file prompt" })}
        placeholder={intl.formatMessage({ id: "web.routes.tabs.files.config_yaml", defaultMessage: "config.yaml", description: "Files tab: example placeholder in the new file name field; an example name, not a default" })}
        confirmLabel={intl.formatMessage({ id: "web.routes.tabs.files.create", defaultMessage: "Create", description: "Files tab: confirm button of the new file and new folder prompts that creates the item; keep it short" })}
        busy={newFileMutation.isPending}
        onSubmit={(value) => newFileMutation.mutate(value)}
      />
    </div>
  );
}

function BreadcrumbsNav({
  cwd,
  onNavigate,
}: {
  cwd: string;
  onNavigate: (p: string) => void;
}): ReactNode {
  const intl = useOptionalIntl();
  const segments = cwd === ROOT ? [] : cwd.split("/").filter(Boolean);
  return (
    <div className="flex items-center gap-0.5 rounded-lg border border-divider bg-background px-3 py-1.5">
      <button
        type="button"
        className="text-default-500 hover:text-foreground text-xs font-mono"
        onClick={() => onNavigate(ROOT)}
      >
        {intl.formatMessage({ id: "web.routes.tabs.files.root_crumb", defaultMessage: "/", description: "Files: breadcrumb button that jumps to the server data root, shown as a slash" })}
      </button>
      {segments.map((seg, i) => {
        const path = "/" + segments.slice(0, i + 1).join("/");
        const isLast = i === segments.length - 1;
        return (
          <span key={path} className="flex items-center gap-0.5">
            <button
              type="button"
              className={cn(
                "text-xs font-mono hover:text-foreground",
                isLast
                  ? "font-medium text-foreground"
                  : "text-default-500",
              )}
              onClick={() => onNavigate(path)}
            >
              {seg}
            </button>
            {!isLast && <span className="text-default-500">{intl.formatMessage({ id: "web.routes.tabs.files.path_separator", defaultMessage: "/", description: "Files: slash drawn between breadcrumb segments" })}</span>}
          </span>
        );
      })}
    </div>
  );
}

interface NamePromptProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  label: string;
  placeholder?: string;
  confirmLabel: string;
  busy?: boolean;
  onSubmit: (value: string) => void;
}

function NamePromptDialog({
  open,
  onOpenChange,
  title,
  label,
  placeholder,
  confirmLabel,
  busy,
  onSubmit,
}: NamePromptProps) {
  const intl = useOptionalIntl();
  const [value, setValue] = useState("");
  // Clear the input whenever the dialog transitions to open. Adjusted
  // directly during render (not in an effect), gated on the previous
  // `open` value tracked in state.
  const [prevOpen, setPrevOpen] = useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (open) setValue("");
  }
  const trimmed = value.trim();
  // Reject empty, slashes, and dot-only names — keeps the prompt aligned with
  // what the agent's resolve() will accept anyway, so users get instant feedback.
  const valid = trimmed.length > 0 && !trimmed.includes("/") && trimmed !== "." && trimmed !== "..";
  // Explain a rejected name instead of only disabling Create (design oMuyw).
  const showError = trimmed.length > 0 && !valid;
  return (
    <Modal isOpen={open} onOpenChange={onOpenChange}>
      <ModalBackdrop isDismissable={!busy}>
        <ModalContainer>
          <ModalDialog>
            <ModalHeader className="flex flex-col gap-1">{title}</ModalHeader>
            <ModalBody>
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  if (valid && !busy) onSubmit(trimmed);
                }}
                className="space-y-4"
              >
                <div>
                  <label className="block pb-2 text-xs text-default-500">{label}</label>
                  <Input
                    autoFocus
                    value={value}
                    placeholder={placeholder}
                    onChange={(e) => setValue(e.target.value)}
                    spellCheck={false}
                    aria-invalid={showError || undefined}
                    aria-describedby={showError ? "name-prompt-error" : undefined}
                    className={cn(showError && "border-danger")}
                  />
                  {showError && (
                    <p id="name-prompt-error" className="pt-1 text-xs text-danger">{intl.formatMessage({ id: "web.routes.tabs.files.names_can_t_contain_or_be_or", defaultMessage: "Names can't contain \"/\" or be \".\" or \"..\".", description: "Files tab: validation error under a file or folder name field when the name contains a slash or is a single or double dot" })}</p>
                  )}
                </div>
              </form>
            </ModalBody>
            <ModalFooter>
              <Button
                variant="ghost"
                onPress={() => onOpenChange(false)}
                isDisabled={busy}
              >{intl.formatMessage({ id: "web.routes.tabs.files.cancel", defaultMessage: "Cancel", description: "Files tab: button in the name prompt dialog that closes it without creating anything; keep it short" })}</Button>
              <Button
                variant="primary"
                isDisabled={!valid || busy}
                onPress={() => {
                  if (valid && !busy) onSubmit(trimmed);
                }}
              >
                {busy ? intl.formatMessage({ id: "web.routes.tabs.files.working", defaultMessage: "Working…", description: "Files: label of the dialog submit button while the request is running" }) : confirmLabel}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}

function joinPath(dir: string, name: string): string {
  if (dir === ROOT) return ROOT + name;
  return dir.replace(/\/$/, "") + "/" + name;
}

function parentOf(p: string): string {
  if (p === ROOT || p === "") return ROOT;
  const trimmed = p.replace(/\/$/, "");
  const idx = trimmed.lastIndexOf("/");
  return idx <= 0 ? ROOT : trimmed.slice(0, idx);
}

function guessLang(name: string): string {
  if (name.endsWith(".yaml") || name.endsWith(".yml")) return "yaml";
  if (name.endsWith(".json")) return "json";
  if (name.endsWith(".properties")) return "ini";
  if (name.endsWith(".toml")) return "toml";
  if (name.endsWith(".sh")) return "shell";
  if (/\.(?:[cm]?js|jsx)$/i.test(name)) return "javascript";
  if (/\.(?:[cm]?ts|tsx)$/i.test(name)) return "typescript";
  if (name.endsWith(".md")) return "markdown";
  return "plaintext";
}
