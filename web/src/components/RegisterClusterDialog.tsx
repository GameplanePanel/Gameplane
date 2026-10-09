import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button, Input, Label, Modal, ModalBackdrop, ModalContainer, ModalDialog, ModalHeader, ModalHeading, ModalBody, ModalFooter, TextArea } from "@heroui/react";
import { Clusters } from "@/lib/endpoints";
import { APIError } from "@/lib/api";

export function RegisterClusterDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
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
        <ModalHeader><ModalHeading>Register cluster</ModalHeading></ModalHeader>
        <ModalBody className="gap-4">
          <p className="text-sm text-muted">Connect an existing workload cluster with the Gameplane operator and CRDs installed. The panel host does not become a workload node.</p>
          <div className="space-y-1"><Label htmlFor="cluster-name">Name</Label><Input id="cluster-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="production" /></div>
          <div className="space-y-1"><Label htmlFor="cluster-display-name">Display name</Label><Input id="cluster-display-name" value={displayName} onChange={(event) => setDisplayName(event.target.value)} /></div>
          <div className="space-y-1"><Label htmlFor="cluster-kubeconfig">Kubeconfig</Label><TextArea id="cluster-kubeconfig" value={kubeconfig} onChange={(event) => setKubeconfig(event.target.value)} rows={8} className="font-mono text-xs" /></div>
          <p className="text-xs text-muted">Use a self-contained kubeconfig with credentials and an API address reachable from this panel. Agent console, files and logs also require remote gateway configuration.</p>
          {register.error && <p role="alert" className="text-sm text-danger">{register.error instanceof APIError ? register.error.body || register.error.message : register.error.message}</p>}
        </ModalBody>
        <ModalFooter><Button variant="outline" isDisabled={register.isPending} onPress={() => onOpenChange(false)}>Cancel</Button><Button isDisabled={register.isPending || !name.trim() || !kubeconfig.trim()} onPress={() => register.mutate()}>Register</Button></ModalFooter>
      </ModalDialog></ModalContainer>
    </ModalBackdrop>
  </Modal>;
}
