import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Button, Card, CardContent, ListBox, ListBoxItem, Select } from "@heroui/react";
import { Plus } from "lucide-react";
import { defineMessages, type MessageDescriptor } from "react-intl";
import { APIError } from "@/lib/api";
import { can, useMe } from "@/lib/auth";
import { Cluster, Clusters } from "@/lib/endpoints";
import { useInstallation } from "@/lib/useInstallation";
import { useOptionalIntl } from "@/lib/i18n/format";

const messages: Record<string, MessageDescriptor> = defineMessages({
  add: { id: "web.nodes.enrollment.add", defaultMessage: "Add node", description: "Nodes: opens node enrollment or creates a command for the selected local cluster" },
  choose: { id: "web.nodes.enrollment.choose", defaultMessage: "Choose a cluster before enrolling a node.", description: "Nodes: prompt requiring an explicit enrollment cluster" },
  target: { id: "web.nodes.enrollment.target", defaultMessage: "Enrollment cluster", description: "Nodes: accessible label for enrollment cluster selection" },
  generate: { id: "web.nodes.enrollment.generate", defaultMessage: "Generate join command", description: "Nodes: creates the join command after selecting an enrollment cluster" },
  remote: { id: "web.nodes.enrollment.remote", defaultMessage: "Node enrollment is managed at this remote cluster.", description: "Nodes: explanation for unsupported remote enrollment" },
  noLocal: { id: "web.nodes.enrollment.no_local", defaultMessage: "No local workload cluster is available on this installation.", description: "Nodes: local enrollment is unavailable on a standalone panel" },
  forbidden: { id: "web.nodes.enrollment.forbidden", defaultMessage: "You don't have permission to enroll nodes.", description: "Nodes: enrollment permission requirement" },
  disabled: { id: "web.nodes.enrollment.disabled", defaultMessage: "Enable clusterOps.enabled in the Helm values to mint node-join tokens.", description: "Nodes: explanation when cluster operations are disabled; clusterOps.enabled is a machine configuration key" },
  notEnabled: { id: "web.nodes.enrollment.not_enabled", defaultMessage: "Cluster operations aren't enabled on this install (set clusterOps.enabled).", description: "Nodes: enrollment error for a disabled cluster-operations endpoint" },
  denied: { id: "web.nodes.enrollment.denied", defaultMessage: "You don't have permission to do that.", description: "Nodes: enrollment request was denied" },
  failed: { id: "web.nodes.enrollment.failed", defaultMessage: "That operation failed. Check the API logs.", description: "Nodes: generic enrollment failure" },
  capabilities: { id: "web.nodes.enrollment.capabilities", defaultMessage: "Couldn't load installation capabilities.", description: "Nodes: enrollment unavailable because installation capabilities failed" },
  clusters: { id: "web.nodes.enrollment.clusters", defaultMessage: "Couldn't load registered clusters.", description: "Nodes: enrollment cluster discovery failed" },
  empty: { id: "web.nodes.enrollment.empty", defaultMessage: "Register a workload cluster on the Clusters page to enroll nodes.", description: "Nodes: no registered enrollment targets exist" },
  dismiss: { id: "web.nodes.enrollment.dismiss", defaultMessage: "Dismiss", description: "Nodes: closes the enrollment result or cluster chooser" },
  copy: { id: "web.nodes.enrollment.copy", defaultMessage: "Copy", description: "Nodes: copies the generated join command" },
  copyFailed: { id: "web.nodes.enrollment.copy_failed", defaultMessage: "Couldn't copy the command. Select and copy it manually.", description: "Nodes: clipboard failure" },
  join: { id: "web.nodes.enrollment.join", defaultMessage: "Join a node to {cluster}", description: "Nodes: title of a generated command, with explicit cluster identity" },
  instruction: { id: "web.nodes.enrollment.instruction", defaultMessage: "Run this on the machine you want to join (token expires {expiresAt}):", description: "Nodes: join command instructions and token expiration" },
});

export function NodeEnrollment({ clusterId }: { clusterId?: string }) {
  // Remounting discards tokens, errors and pending results when route scope changes.
  return <EnrollmentScope key={JSON.stringify(clusterId || "")} clusterId={clusterId || ""} />;
}

function EnrollmentScope({ clusterId }: { clusterId: string }) {
  const intl = useOptionalIntl();
  const { data: me, isPending: identityPending } = useMe();
  const installation = useInstallation();
  const allowed = can(me, "cluster:manage");
  const [open, setOpen] = useState(false);
  const [target, setTarget] = useState(clusterId);
  const registry = useQuery({ queryKey: ["clusters"], queryFn: () => Clusters.list(), enabled: !clusterId && open && allowed });
  const targets = (registry.data?.items ?? []).filter((cluster) => cluster.name !== "local" || installation.data?.localCluster === true);
  const activeTarget = clusterId || target;
  return <div className="flex w-full min-w-0 flex-col items-start gap-2 sm:items-end">
    {!clusterId && <Button isDisabled={identityPending || !allowed || installation.isPending || installation.isError} onPress={() => setOpen(!open)}><Plus className="h-4 w-4" />{intl.formatMessage(messages.add)}</Button>}
    {!clusterId && open && <Card className="w-full max-w-md"><CardContent className="space-y-3 p-4">
      <p className="text-sm">{intl.formatMessage(messages.choose)}</p>
      <Select aria-label={intl.formatMessage(messages.target)} placeholder={intl.formatMessage(messages.target)} value={target || null} onChange={(value) => setTarget(String(value ?? ""))}>
        <Select.Trigger><Select.Value /><Select.Indicator className="ml-auto" /></Select.Trigger>
        <Select.Popover><ListBox>{targets.map((cluster) => <ListBoxItem id={cluster.name} key={cluster.name} textValue={cluster.displayName || cluster.name}>{cluster.displayName || cluster.name}</ListBoxItem>)}</ListBox></Select.Popover>
      </Select>
      {registry.isError && <p role="alert" className="text-sm text-danger">{intl.formatMessage(messages.clusters)}</p>}
      {registry.isSuccess && !targets.length && <p className="text-sm text-muted">{intl.formatMessage(messages.empty)}</p>}
      <Button size="sm" variant="outline" onPress={() => { setOpen(false); setTarget(""); }}>{intl.formatMessage(messages.dismiss)}</Button>
    </CardContent></Card>}
    {(clusterId || open && activeTarget) && <EnrollmentTarget key={activeTarget} clusterId={activeTarget} choosing={!clusterId} registered={!!clusterId || targets.some((cluster) => cluster.name === activeTarget)} />}
    {!clusterId && !allowed && !identityPending && <p className="text-xs text-muted">{intl.formatMessage(messages.forbidden)}</p>}
    {!clusterId && installation.isError && <p role="alert" className="text-xs text-danger">{intl.formatMessage(messages.capabilities)}</p>}
  </div>;
}

function EnrollmentTarget({ clusterId, choosing, registered }: { clusterId: string; choosing: boolean; registered: boolean }) {
  const intl = useOptionalIntl();
  const { data: me, isPending: identityPending } = useMe();
  const installation = useInstallation();
  const allowed = can(me, "cluster:manage");
  const local = clusterId === "local" && installation.data?.localCluster === true;
  const info = useQuery({ queryKey: ["cluster-info", clusterId], queryFn: ({ signal }) => Cluster.info(clusterId, signal), enabled: local && allowed });
  const [copyFailed, setCopyFailed] = useState(false);
  const add = useMutation({ mutationFn: (capturedCluster: string) => Cluster.addNode(capturedCluster) });
  const disabled = !registered || !allowed || !local || info.isPending || info.data?.clusterOps === false || add.isPending;
  const reason = !identityPending && !allowed ? messages.forbidden : clusterId !== "local" ? messages.remote
    : installation.isError ? messages.capabilities : installation.isSuccess && !local ? messages.noLocal
      : info.data?.clusterOps === false ? messages.disabled : undefined;
  const error = add.error instanceof APIError && add.error.status === 501 ? messages.notEnabled : add.error instanceof APIError && add.error.status === 403 ? messages.denied : messages.failed;
  async function copy() {
    try {
      if (!navigator.clipboard) throw new Error("Clipboard unavailable");
      await navigator.clipboard.writeText(add.data?.command ?? "");
      setCopyFailed(false);
    } catch { setCopyFailed(true); }
  }
  return <div className="w-full min-w-0 space-y-2 sm:text-right">
    <Button isDisabled={disabled} onPress={() => { setCopyFailed(false); add.mutate(clusterId); }}><Plus className="h-4 w-4" />{intl.formatMessage(choosing ? messages.generate : messages.add)}</Button>
    {reason && <p className="max-w-md text-xs text-muted">{intl.formatMessage(reason)}</p>}
    {add.error && <p role="alert" className="text-sm text-danger">{intl.formatMessage(error)}</p>}
    {add.data && <Card className="w-full max-w-md text-left"><CardContent className="space-y-3 p-4">
      <div className="flex items-start justify-between gap-3"><h2 className="break-all text-sm font-medium">{intl.formatMessage(messages.join, { cluster: clusterId })}</h2><Button size="sm" variant="outline" onPress={() => { add.reset(); setCopyFailed(false); }}>{intl.formatMessage(messages.dismiss)}</Button></div>
      <p className="text-xs text-muted">{intl.formatMessage(messages.instruction, { expiresAt: add.data.expiresAt })}</p>
      <code className="block max-h-40 overflow-auto break-all rounded bg-surface px-3 py-2 font-mono text-xs">{add.data.command}</code>
      <Button size="sm" variant="outline" onPress={() => void copy()}>{intl.formatMessage(messages.copy)}</Button>
      {copyFailed && <p role="alert" className="text-xs text-danger">{intl.formatMessage(messages.copyFailed)}</p>}
    </CardContent></Card>}
  </div>;
}
