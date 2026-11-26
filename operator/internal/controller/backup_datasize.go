package controller

import (
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// backupDataSizeMaxAgentAge bounds how old the agent's disk sample may be
// when a CSI snapshot completes; older samples are not recorded.
const backupDataSizeMaxAgentAge = 10 * time.Minute

// agentDataSize returns the game data usage the agent last reported for gs,
// or nil when it is unknown or the heartbeat is older than
// backupDataSizeMaxAgentAge. It never falls back to the volume capacity.
func agentDataSize(gs *gameplanev1alpha1.GameServer, now time.Time) *resource.Quantity {
	if gs == nil || gs.Status.Agent == nil {
		return nil
	}
	a := gs.Status.Agent
	if a.DiskUsedBytes == nil || *a.DiskUsedBytes < 0 || a.LastHeartbeat == nil {
		return nil
	}
	if now.Sub(a.LastHeartbeat.Time) > backupDataSizeMaxAgentAge {
		return nil
	}
	return resource.NewQuantity(*a.DiskUsedBytes, resource.BinarySI)
}
