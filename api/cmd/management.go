package main

import (
	"context"
	"fmt"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/GameplanePanel/gameplane/api/internal/controlplane"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// managementClients separates panel storage from workload clients. Standalone
// never consults in-cluster credentials or the host's kubeconfig.
func managementClients(ctx context.Context, cfg config, store *db.Store) (*kube.Client, *kube.Registry, error) {
	reg := kube.NewRegistry(scope.DefaultCluster)
	if cfg.standalone {
		if cfg.clusterOps {
			return nil, nil, fmt.Errorf("cluster-ops requires a local Kubernetes cluster")
		}
		var cidrs []string
		if strings.TrimSpace(cfg.remoteAllowedCIDRs) != "" {
			cidrs = strings.Split(cfg.remoteAllowedCIDRs, ",")
		}
		policy, err := kube.NewRemoteAccessPolicy(cidrs)
		if err != nil {
			return nil, nil, fmt.Errorf("standalone remote policy: %w", err)
		}
		management, err := controlplane.NewWithOptions(ctx, store, controlplane.KeyOptions{
			File: cfg.panelKeyFile, Provisioned: cfg.panelKeyProvisioned,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("standalone storage: %w", err)
		}
		management.RemoteAccess = policy
		reg.SetManagement(management)
		return management, reg, nil
	}
	restCfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("get kubeconfig: %w", err)
	}
	local, err := kube.New(restCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("kube client: %w", err)
	}
	reg.Set(scope.DefaultCluster, local)
	reg.SetManagement(local)
	return local, reg, nil
}
