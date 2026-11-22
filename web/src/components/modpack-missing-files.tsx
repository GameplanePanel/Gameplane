import type { ReactNode } from "react";
import { Button, Table } from "@heroui/react";
import { ExternalLink } from "lucide-react";
import type { ModpackView } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";

// ModpackMissingFiles tells the operator which files of a CurseForge modpack
// the server cannot download itself (the author blocks third-party downloads),
// and where to upload them. Renders nothing when there is nothing to report.
export function ModpackMissingFiles({
  view,
  onOpenFiles,
}: {
  view: ModpackView;
  onOpenFiles?: () => void;
}) {
  const intl = useOptionalIntl();

  if (view.missingFilesUnknown) {
    return (
      <p className="text-xs text-muted">
        {intl.formatMessage({ id: "web.components.modpack_missing_files.unknown", defaultMessage: "Start the server to check whether this pack needs files you have to download yourself.", description: "Modpack missing files: info note shown when the server must start before the missing files can be known" })}
      </p>
    );
  }
  if (view.missingFiles.length === 0) return null;

  const count = view.missingFiles.length;

  return (
    <section className="flex flex-col gap-3 rounded-xl border border-warning/40 bg-warning/10 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 space-y-1">
          <h3 className="text-sm font-medium">
            {intl.formatMessage({ id: "web.components.modpack_missing_files.title", defaultMessage: "{count, plural, one {# file needs} other {# files need}} a manual download", description: "Modpack missing files: panel title saying how many files must be downloaded by hand; {count} is the number of files", }, { count })}
          </h3>
          <p className="text-xs text-muted">
            {intl.formatMessage(
              { id: "web.components.modpack_missing_files.body", defaultMessage: "Their authors block automatic downloads, so the install stopped. Download each file from its page, upload it to <code>{path}</code> in the Files tab, then restart the server.", description: "Modpack missing files: explains that files blocked by their authors must be downloaded and uploaded by hand; {path} is the downloads folder, shown in code style" },
              { path: view.downloadsPath, code: (chunks: ReactNode) => <code className="font-mono text-foreground">{chunks}</code> },
            )}
          </p>
        </div>
        {onOpenFiles && (
          <Button variant="outline" size="sm" onPress={onOpenFiles}>
            {intl.formatMessage({ id: "web.components.modpack_missing_files.open_files", defaultMessage: "Open Files", description: "Modpack missing files: button that switches to the Files tab so the operator can upload the files" })}
          </Button>
        )}
      </div>
      <Table.Root>
        <Table.ScrollContainer>
          <Table.Content aria-label={intl.formatMessage({ id: "web.components.modpack_missing_files.table", defaultMessage: "Missing files table", description: "Modpack missing files: accessible label of the table listing the files to download, read by screen readers; not shown as text" })} keyboardNavigationBehavior="arrow">
            <Table.Header>
              <Table.Column key="mod" isRowHeader>{intl.formatMessage({ id: "web.components.modpack_missing_files.mod", defaultMessage: "Mod", description: "Modpack missing files: column heading for the mod name; keep it short" })}</Table.Column>
              <Table.Column key="file">{intl.formatMessage({ id: "web.components.modpack_missing_files.file_name", defaultMessage: "File name", description: "Modpack missing files: column heading for the file name to upload; keep it short" })}</Table.Column>
              <Table.Column key="download">{intl.formatMessage({ id: "web.components.modpack_missing_files.download", defaultMessage: "Download", description: "Modpack missing files: column heading for the link to the file's download page; keep it short" })}</Table.Column>
            </Table.Header>
            <Table.Body>
              {view.missingFiles.map((f) => (
                <Table.Row key={`${f.mod}|${f.filename}`} id={`${f.mod}|${f.filename}`}>
                  <Table.Cell>{f.mod}</Table.Cell>
                  <Table.Cell className="font-mono">{f.filename}</Table.Cell>
                  <Table.Cell>
                    {f.downloadPage.startsWith("https://") ? (
                      <a href={f.downloadPage} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-accent hover:underline">
                        {intl.formatMessage({ id: "web.components.modpack_missing_files.download_page", defaultMessage: "Download page", description: "Modpack missing files: link text that opens the file's download page in a new tab" })}
                        <ExternalLink className="h-3 w-3" aria-hidden="true" />
                      </a>
                    ) : (
                      <span className="text-muted">
                        {intl.formatMessage({ id: "web.components.modpack_missing_files.download_page", defaultMessage: "Download page", description: "Modpack missing files: link text that opens the file's download page in a new tab" })}
                      </span>
                    )}
                  </Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table.Content>
        </Table.ScrollContainer>
      </Table.Root>
    </section>
  );
}
