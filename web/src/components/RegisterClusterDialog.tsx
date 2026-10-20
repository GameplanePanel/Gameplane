import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button, Input, Label, Modal, ModalBackdrop, ModalContainer, ModalDialog, ModalHeader, ModalHeading, ModalBody, ModalFooter, TextArea } from "@heroui/react";
import { Clusters } from "@/lib/endpoints";
import { APIError } from "@/lib/api";
import { useOptionalIntl } from "@/lib/i18n/format";

export function RegisterClusterDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const intl = useOptionalIntl();
  const client = useQueryClient();
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [kubeconfig, setKubeconfig] = useState("");
  const register = useMutation({
    mutationFn: () => Clusters.register({ name: name.trim(), displayName: displayName.trim(), kubeconfig }),
    onSuccess: async () => {
      setKubeconfig("");
      onOpenChange(false);
      await client.invalidateQueries({ queryKey: ["clusters"] });
      await client.invalidateQueries({ queryKey: ["fleet"] });
    },
  });
  return <Modal isOpen={open} onOpenChange={onOpenChange}>
    <ModalBackdrop isDismissable={!register.isPending} isKeyboardDismissDisabled={register.isPending}>
      <ModalContainer><ModalDialog className="w-[480px] max-w-full">
        <ModalHeader><ModalHeading>{intl.formatMessage({ id: "web.components.register_cluster_dialog.register_cluster", defaultMessage: "Register cluster", description: "Register cluster dialog: title of the dialog for adding an existing workload cluster to the panel" })}</ModalHeading></ModalHeader>
        <ModalBody className="gap-4">
          <p className="text-sm text-muted">{intl.formatMessage({ id: "web.components.register_cluster_dialog.connect_an_existing_workload_cluster", defaultMessage: "Connect an existing workload cluster with the Gameplane operator and CRDs installed. The panel host does not become a workload node.", description: "Register cluster dialog: intro under the title saying the cluster needs the Gameplane operator and CRDs installed and that the panel host does not become a workload node" })}</p>
          <div className="space-y-1"><Label htmlFor="cluster-name">{intl.formatMessage({ id: "web.components.register_cluster_dialog.name", defaultMessage: "Name", description: "Register cluster dialog: label for the cluster name field, the short identifier of the cluster" })}</Label><Input id="cluster-name" value={name} onChange={(event) => setName(event.target.value)} placeholder={intl.formatMessage({ id: "web.components.register_cluster_dialog.production", defaultMessage: "production", description: "Register cluster dialog: example value shown inside the empty cluster name field; a sample name, not a default value" })} /></div>
          <div className="space-y-1"><Label htmlFor="cluster-display-name">{intl.formatMessage({ id: "web.components.register_cluster_dialog.display_name", defaultMessage: "Display name", description: "Register cluster dialog: label for the cluster display name field" })}</Label><Input id="cluster-display-name" value={displayName} onChange={(event) => setDisplayName(event.target.value)} /></div>
          <div className="space-y-1"><Label htmlFor="cluster-kubeconfig">{intl.formatMessage({ id: "web.components.register_cluster_dialog.kubeconfig", defaultMessage: "Kubeconfig", description: "Register cluster dialog: label for the large text box where the cluster's kubeconfig is pasted; technical term, usually left untranslated" })}</Label><TextArea id="cluster-kubeconfig" value={kubeconfig} onChange={(event) => setKubeconfig(event.target.value)} rows={8} className="font-mono text-xs" /></div>
          <p className="text-xs text-muted">{intl.formatMessage({ id: "web.components.register_cluster_dialog.use_a_self_contained_kubeconfig_with", defaultMessage: "Use a self-contained kubeconfig with credentials and an API address reachable from this panel. Agent console, files and logs also require remote gateway configuration.", description: "Register cluster dialog: hint under the kubeconfig box saying the file must be self-contained with an API address this panel can reach, and that console, files and logs also need gateway setup" })}</p>
          {register.error && <p role="alert" className="text-sm text-danger">{register.error instanceof APIError ? register.error.body || register.error.message : register.error.message}</p>}
        </ModalBody>
        <ModalFooter><Button variant="outline" isDisabled={register.isPending} onPress={() => onOpenChange(false)}>{intl.formatMessage({ id: "web.components.register_cluster_dialog.cancel", defaultMessage: "Cancel", description: "Register cluster dialog: button that closes the dialog without registering the cluster; keep it short" })}</Button><Button isDisabled={register.isPending || !name.trim() || !kubeconfig.trim()} onPress={() => register.mutate()}>{intl.formatMessage({ id: "web.components.register_cluster_dialog.register", defaultMessage: "Register", description: "Register cluster dialog: button that submits the form and registers the cluster; keep it short" })}</Button></ModalFooter>
      </ModalDialog></ModalContainer>
    </ModalBackdrop>
  </Modal>;
}
