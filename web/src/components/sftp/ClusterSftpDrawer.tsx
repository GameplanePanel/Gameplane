// Cluster SFTP drawer (spec 040, US4; design Screen/Clusters — SFTP (remote-lab) 309702331-255,
// token created 309702331-453, token limit 309702331-643). Opened from a cluster card on Clusters by a
// user with cluster:manage. It sets the remote cluster's SFTP endpoint (address and host key fingerprint),
// lists its active endpoint tokens, creates a token (shown once) and revokes one. The panel's own cluster
// reads its endpoint from Helm values and the host key Secret, so its endpoint is shown read-only.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Button,
  Description,
  Input,
  Label,
  Modal,
  ModalBackdrop,
  ModalBody,
  ModalContainer,
  ModalDialog,
  ModalFooter,
  ModalHeader,
  ModalHeading,
} from "@heroui/react";
import { AlertCircle, Copy, Trash2 } from "lucide-react";
import { errorTextIntl } from "@/lib/errors";
import { formatRelative } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";
import {
  CLUSTER_SFTP_TOKEN_LIMIT,
  createClusterSftpToken,
  getClusterSftp,
  putClusterSftp,
  revokeClusterSftpToken,
  type ClusterSftpEndpoint,
  type ClusterSftpTokenCreated,
  type ClusterSftpView,
} from "@/lib/api";

// Same shape the API checks (sftpFingerprintPattern in sftp_cluster.go). Checked here for immediate feedback.
const FINGERPRINT_PATTERN = /^SHA256:[A-Za-z0-9+/]{43}$/;

export function ClusterSftpDrawer({
  cluster,
  label,
  onClose,
}: {
  cluster: string;
  label: string;
  onClose: () => void;
}) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const queryKey = ["clusters", cluster, "sftp"] as const;
  // Form fields stay undefined until the admin edits them, so the saved values show until then.
  const [addressDraft, setAddressDraft] = useState<string | undefined>(undefined);
  const [fingerprintDraft, setFingerprintDraft] = useState<string | undefined>(undefined);
  const [created, setCreated] = useState<ClusterSftpTokenCreated | null>(null);

  const { data, error, isPending } = useQuery({
    queryKey,
    queryFn: () => getClusterSftp(cluster),
  });

  const refresh = () => {
    void qc.invalidateQueries({ queryKey });
  };

  const save = useMutation({
    mutationFn: (body: { address: string; hostKeyFingerprint: string }) => putClusterSftp(cluster, body),
    onSuccess: (endpoint: ClusterSftpEndpoint) => {
      qc.setQueryData<ClusterSftpView>(queryKey, (prev) =>
        prev ? { ...prev, address: endpoint.address, hostKeyFingerprint: endpoint.hostKeyFingerprint } : prev,
      );
      setAddressDraft(undefined);
      setFingerprintDraft(undefined);
      refresh();
    },
  });

  const createToken = useMutation({
    mutationFn: () => createClusterSftpToken(cluster),
    onSuccess: (token) => {
      setCreated(token);
      refresh();
    },
  });

  const revoke = useMutation({
    mutationFn: (tokenId: number) => revokeClusterSftpToken(cluster, tokenId),
    onSuccess: refresh,
  });

  const address = addressDraft ?? data?.address ?? "";
  const fingerprint = fingerprintDraft ?? data?.hostKeyFingerprint ?? "";
  const tokenCount = data?.tokens.length ?? 0;
  const atLimit = tokenCount >= CLUSTER_SFTP_TOKEN_LIMIT;
  const canSave = address.trim() !== "" && FINGERPRINT_PATTERN.test(fingerprint.trim()) && !save.isPending;
  const actionError = save.error ?? createToken.error ?? revoke.error;
  const actionFallback = save.error
    ? intl.formatMessage({ id: "web.components.sftp.cluster_drawer.save_failed", defaultMessage: "The endpoint could not be saved.", description: "Cluster SFTP drawer: fallback error when saving the endpoint fails" })
    : createToken.error
      ? intl.formatMessage({ id: "web.components.sftp.cluster_drawer.create_failed", defaultMessage: "The token could not be created.", description: "Cluster SFTP drawer: fallback error when creating an endpoint token fails" })
      : intl.formatMessage({ id: "web.components.sftp.cluster_drawer.revoke_failed", defaultMessage: "The token could not be revoked.", description: "Cluster SFTP drawer: fallback error when revoking an endpoint token fails" });

  return (
    <>
      <Modal isOpen onOpenChange={(open) => !open && onClose()}>
        <ModalBackdrop>
          <ModalContainer>
            <ModalDialog className="ml-auto h-full w-[460px] max-w-full rounded-none">
              <ModalHeader>
                <ModalHeading>
                  {intl.formatMessage({ id: "web.components.sftp.cluster_drawer.title", defaultMessage: "SFTP · {cluster}", description: "Cluster SFTP drawer: heading; {cluster} is the cluster display name" }, { cluster: label })}
                </ModalHeading>
                <Description className="text-sm text-muted">
                  {intl.formatMessage({ id: "web.components.sftp.cluster_drawer.description", defaultMessage: "Users connect to this address with their own SSH key or SFTP password. Their permissions on this cluster decide what they can reach.", description: "Cluster SFTP drawer: explains that users connect to the address and their cluster permissions decide what they reach" })}
                </Description>
              </ModalHeader>
              <ModalBody className="space-y-6">
                {error ? (
                  <p role="alert" className="text-sm text-danger">
                    {errorTextIntl(intl, error, intl.formatMessage({ id: "web.components.sftp.cluster_drawer.load_failed", defaultMessage: "Could not load the SFTP endpoint.", description: "Cluster SFTP drawer: error when the cluster's SFTP endpoint cannot be loaded" }))}
                  </p>
                ) : isPending || !data ? (
                  <p className="text-sm text-muted">{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.loading", defaultMessage: "Loading…", description: "Cluster SFTP drawer: loading text" })}</p>
                ) : (
                  <>
                    <section className="space-y-4">
                      {data.local ? (
                        <>
                          <p className="text-sm text-muted">{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.local_note", defaultMessage: "This is the panel's own cluster. Its address comes from the chart and its host key fingerprint from the panel's host key.", description: "Cluster SFTP drawer: note for the panel's own cluster, whose endpoint is read-only and comes from the chart and host key" })}</p>
                          <p className="break-all font-mono text-sm">{data.address ?? "—"}</p>
                          <p className="break-all font-mono text-xs">{data.hostKeyFingerprint ?? "—"}</p>
                        </>
                      ) : (
                        <form
                          className="space-y-4"
                          onSubmit={(event) => {
                            event.preventDefault();
                            if (canSave) save.mutate({ address: address.trim(), hostKeyFingerprint: fingerprint.trim() });
                          }}
                        >
                          <div className="space-y-1">
                            <Label htmlFor="cluster-sftp-address">{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.address", defaultMessage: "Address", description: "Cluster SFTP drawer: label for the SFTP address field (host or host:port)" })}</Label>
                            <Input id="cluster-sftp-address" value={address} onChange={(event) => setAddressDraft(event.target.value)} placeholder={intl.formatMessage({ id: "web.components.sftp.cluster_drawer.address_placeholder", defaultMessage: "sftp.example.com:2222", description: "Cluster SFTP drawer: example value shown inside the empty address field; a sample, not a default" })} className="font-mono" />
                          </div>
                          <div className="space-y-1">
                            <Label htmlFor="cluster-sftp-fingerprint">{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.host_key_fingerprint", defaultMessage: "Host key fingerprint (from the cluster registration)", description: "Cluster SFTP drawer: label for the host key fingerprint field; the value comes from the cluster registration" })}</Label>
                            <Input id="cluster-sftp-fingerprint" value={fingerprint} onChange={(event) => setFingerprintDraft(event.target.value)} placeholder="SHA256:" className="font-mono text-xs" />
                          </div>
                          <Button type="submit" isDisabled={!canSave}>{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.save_endpoint", defaultMessage: "Save endpoint", description: "Cluster SFTP drawer: button that saves the cluster's SFTP address and host key fingerprint; keep it short" })}</Button>
                        </form>
                      )}
                    </section>

                    <section className="space-y-3 border-t border-divider pt-4">
                      <h3 className="text-sm font-semibold">
                        {intl.formatMessage({ id: "web.components.sftp.cluster_drawer.tokens_heading", defaultMessage: "Endpoint tokens · {count} of {limit} in use", description: "Cluster SFTP drawer: heading of the endpoint token list with the active count and the per-cluster limit; {count} and {limit} are numbers" }, { count: tokenCount, limit: CLUSTER_SFTP_TOKEN_LIMIT })}
                      </h3>
                      {atLimit && (
                        <div role="alert" className="flex items-start gap-2 rounded-lg border border-danger/40 p-3 text-sm text-danger">
                          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                          <span>
                            {intl.formatMessage({ id: "web.components.sftp.cluster_drawer.limit_message", defaultMessage: "This cluster already has the maximum of {limit} active tokens. Revoke one token before you can create another.", description: "Cluster SFTP drawer: warning shown when the cluster has the maximum number of active tokens; {limit} is a number" }, { limit: CLUSTER_SFTP_TOKEN_LIMIT })}
                          </span>
                        </div>
                      )}
                      {tokenCount === 0 && (
                        <p className="text-sm text-muted">{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.no_tokens", defaultMessage: "No active tokens.", description: "Cluster SFTP drawer: shown when the cluster has no active endpoint token" })}</p>
                      )}
                      {data.tokens.map((t) => (
                        <div key={t.id} className="flex items-start justify-between gap-3">
                          <div className="min-w-0 space-y-1">
                            <p className="break-all font-mono text-sm">{t.prefix}</p>
                            <p className="text-xs text-muted">
                              {intl.formatMessage({ id: "web.components.sftp.cluster_drawer.token_meta", defaultMessage: "Created {created} · {lastUsed}", description: "Cluster SFTP drawer: when an endpoint token was created and when it was last used; {created} and {lastUsed} are relative times" }, { created: formatRelative(t.createdAt, intl), lastUsed: t.lastUsedAt ? intl.formatMessage({ id: "web.components.sftp.cluster_drawer.last_used", defaultMessage: "Last used {time}", description: "Cluster SFTP drawer: when a token was last used; {time} is a relative time" }, { time: formatRelative(t.lastUsedAt, intl) }) : intl.formatMessage({ id: "web.components.sftp.cluster_drawer.never_used", defaultMessage: "Never used", description: "Cluster SFTP drawer: a token that has never been used" }) })}
                            </p>
                          </div>
                          <Button size="sm" variant="danger" className="shrink-0" isDisabled={revoke.isPending} onPress={() => revoke.mutate(t.id)}>
                            <Trash2 className="h-4 w-4" />{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.revoke", defaultMessage: "Revoke", description: "Cluster SFTP drawer: button that revokes one endpoint token; keep it short" })}
                          </Button>
                        </div>
                      ))}
                      <Button size="sm" variant="outline" isDisabled={atLimit || createToken.isPending} onPress={() => createToken.mutate()}>
                        {intl.formatMessage({ id: "web.components.sftp.cluster_drawer.create_token", defaultMessage: "Create token", description: "Cluster SFTP drawer: button that creates a new endpoint token; disabled at the token limit; keep it short" })}
                      </Button>
                      <p className="text-xs text-muted">
                        {intl.formatMessage({ id: "web.components.sftp.cluster_drawer.token_limit_hint", defaultMessage: "A cluster can have at most {limit} active tokens. Revoking a token stops that endpoint from logging users in.", description: "Cluster SFTP drawer: explains the per-cluster token limit and what revoking does; {limit} is a number" }, { limit: CLUSTER_SFTP_TOKEN_LIMIT })}
                      </p>
                      <p className="text-xs text-muted">
                        {intl.formatMessage({ id: "web.components.sftp.cluster_drawer.token_use_hint", defaultMessage: "Used only by this cluster's SFTP server to reach the central API. Never give these to users.", description: "Cluster SFTP drawer: explains that endpoint tokens are for the cluster's SFTP server only" })}
                      </p>
                    </section>
                  </>
                )}
                {actionError && (
                  <p role="alert" className="text-sm text-danger">
                    {errorTextIntl(intl, actionError, actionFallback)}
                  </p>
                )}
              </ModalBody>
              <ModalFooter className="flex items-center justify-end gap-2">
                <Button size="sm" variant="outline" onPress={onClose}>{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.cancel", defaultMessage: "Cancel", description: "Cluster SFTP drawer: button that closes the drawer without further changes; keep it short" })}</Button>
              </ModalFooter>
            </ModalDialog>
          </ModalContainer>
        </ModalBackdrop>
      </Modal>
      {created && <ClusterSftpTokenShownOnce token={created.token} onDone={() => setCreated(null)} />}
    </>
  );
}

// Shown once, right after a token is created. Gameplane keeps only a hash, so the secret is lost on close.
function ClusterSftpTokenShownOnce({ token, onDone }: { token: string; onDone: () => void }) {
  const intl = useOptionalIntl();
  const [copied, setCopied] = useState(false);
  const copy = () => {
    try {
      void navigator.clipboard.writeText(token).then(
        () => setCopied(true),
        () => setCopied(false),
      );
    } catch {
      setCopied(false);
    }
  };

  return (
    <Modal isOpen>
      <ModalBackdrop isDismissable={false} isKeyboardDismissDisabled>
        <ModalContainer>
          <ModalDialog className="w-[480px] max-w-full">
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.created_title", defaultMessage: "Endpoint token created", description: "Cluster SFTP token shown-once dialog: heading" })}</ModalHeading>
              <Description className="text-sm text-muted">{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.created_description", defaultMessage: "Copy it now. Gameplane stores only a hash and cannot show it again.", description: "Cluster SFTP token shown-once dialog: instruction under the heading" })}</Description>
            </ModalHeader>
            <ModalBody className="space-y-4">
              <div role="alert" className="flex items-start gap-2 rounded-lg border border-danger/40 p-3 text-sm text-danger">
                <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                <span>{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.created_warning", defaultMessage: "You will not see this again. Put it in the remote cluster's SFTP secret before you close this dialog.", description: "Cluster SFTP token shown-once dialog: warning that the token cannot be shown again and must go into the remote cluster's SFTP secret" })}</span>
              </div>
              <code data-testid="cluster-sftp-token-value" className="block break-all rounded-lg bg-default-100 p-3 font-mono text-sm">{token}</code>
              <Button variant="ghost" size="sm" onPress={copy}>
                <Copy className="h-4 w-4" />
                {copied
                  ? intl.formatMessage({ id: "web.components.sftp.cluster_drawer.copied", defaultMessage: "Copied", description: "Cluster SFTP token shown-once dialog: button label after the token was copied" })
                  : intl.formatMessage({ id: "web.components.sftp.cluster_drawer.copy", defaultMessage: "Copy", description: "Cluster SFTP token shown-once dialog: button that copies the token to the clipboard; keep it short" })}
              </Button>
            </ModalBody>
            <ModalFooter className="flex items-center justify-end">
              <Button size="sm" onPress={onDone}>{intl.formatMessage({ id: "web.components.sftp.cluster_drawer.done", defaultMessage: "Done", description: "Cluster SFTP token shown-once dialog: button that closes the dialog; keep it short" })}</Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
