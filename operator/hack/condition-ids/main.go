// Command condition-ids writes the English language-pack entries for operator
// condition messages (ids condition.<type>.<reason>, lower-cased) into the
// shared English reference, and guards that every reason the controllers can
// write has an entry.
//
// The table below is the source of truth for the English text. The guard
// scans the controllers' Go source for the reason literals they set and fails
// when a reason is missing from the table, or when a table reason is no longer
// set anywhere. It reports the mismatch instead of editing the table.
//
// With -ids <file> the generated condition.* ids are also written to that file,
// one per line, for the unused-id check (spec 019).
package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

// reqExpr is the ICU select that names an AddressAssignment request. The table
// writes it as $REQ.
const reqExpr = "{requestKind, select, addressAndPool {address ''{address}'' from pool ''{pool}''} address {address ''{address}''} other {pool ''{pool}''}}"

// row is one table entry. types lists the condition types that share the
// text (for example Ready|Pulling). dynamic rows take their reason from a
// runtime phase string, so the guard does not require them in the source.
type row struct {
	types       []string
	reason      string
	english     string
	description string
	dynamic     bool
}

// guardCalls maps a helper that takes a condition reason as a string literal
// to the index of that argument.
var guardCalls = map[string]int{
	"markUnhealthy":                 2,
	"markFailed":                    2,
	"markPending":                   2,
	"failWithReason":                2,
	"setSnapshotForgottenCondition": 3,
}

// guardReturnFuncs are the functions whose first string result is a reason.
var guardReturnFuncs = map[string]bool{
	"waitingReason":      true,
	"provisioningReason": true,
	"containerFailure":   true,
}

// guardSkipFiles are controller files whose Reason is free text.
var guardSkipFiles = map[string]bool{
	"gameserver_idle.go": true,
}

// rows is the English reference for every condition reason.
var rows = []row{
	// GameServer: Ready
	{types: []string{"Ready"}, reason: "Running", english: "The server is running and the agent is reporting heartbeats.", description: "Ready condition while the game server is up."},
	{types: []string{"Ready"}, reason: "Pending", english: "The server is waiting to start.", description: "Ready condition while the game server is being created."},
	{types: []string{"Ready"}, reason: "Starting", english: "The server is starting.", description: "Ready condition while the pod is starting up."},
	{types: []string{"Ready"}, reason: "Stopping", english: "The server is stopping.", description: "Ready condition while the pod is shutting down."},
	{types: []string{"Ready"}, reason: "Suspended", english: "The server is stopped.", description: "Ready condition for a server that was stopped."},
	{types: []string{"Ready"}, reason: "IdleAsleep", english: "The server is asleep because no players are online.", description: "Ready condition for a server put to sleep by idle auto-sleep."},
	{types: []string{"Ready"}, reason: "Failed", english: "{detail}", description: "Ready condition when the server cannot start. detail is the English reason from the operator, for example a missing template or an invalid configuration."},
	{types: []string{"Ready"}, reason: "Unknown", english: "The state of the server is not known yet.", description: "Ready condition before the operator has observed the server."},
	{types: []string{"Ready"}, reason: "PVCProvisioningFailed", english: "Storage cannot be provisioned: PVC ''{pvc}'' needs StorageClass ''{storageClass}'', which does not exist on this cluster.", description: "Ready condition when the data volume cannot be created. pvc is the volume claim name, storageClass the missing class."},
	{types: []string{"Ready"}, reason: "ImagePullFailed", english: "The game image cannot be pulled. Check the image reference.", description: "Ready condition when the container image pull keeps failing."},
	{types: []string{"Ready"}, reason: "CrashLoopBackOff", english: "The game container keeps crashing during startup ({restartCount} restarts so far). Check the logs.", description: "Ready condition when the game container crash-loops. restartCount is the number of restarts."},
	{types: []string{"Ready"}, reason: "ContainerExited", english: "The game container exited with code {exitCode} during startup. Check the logs.", description: "Ready condition when the game container exits during startup. exitCode is the process exit code."},

	// GameServer: Progressing
	{types: []string{"Progressing"}, reason: "Stable", english: "The server is stable and nothing is changing.", description: "Progressing condition once the server is running."},
	{types: []string{"Progressing"}, reason: "Pending", english: "The server is waiting to be scheduled.", description: "Progressing condition while the pod is waiting for a node."},
	{types: []string{"Progressing"}, reason: "Starting", english: "The server is starting.", description: "Progressing condition while the pod starts."},
	{types: []string{"Progressing"}, reason: "Stopping", english: "The server is stopping.", description: "Progressing condition while the pod stops."},
	{types: []string{"Progressing"}, reason: "Suspended", english: "The server is stopped and nothing is in progress.", description: "Progressing condition for a stopped server."},
	{types: []string{"Progressing"}, reason: "IdleAsleep", english: "The server is asleep because no players are online.", description: "Progressing condition for an idle-sleeping server."},
	{types: []string{"Progressing"}, reason: "Failed", english: "The server stopped trying to start.", description: "Progressing condition when startup has failed."},
	{types: []string{"Progressing"}, reason: "Unknown", english: "The state of the server is not known yet.", description: "Progressing condition before the operator has observed the server."},
	{types: []string{"Progressing"}, reason: "PVCProvisioningFailed", english: "Storage cannot be provisioned: PVC ''{pvc}'' needs StorageClass ''{storageClass}'', which does not exist on this cluster.", description: "Progressing condition when the data volume cannot be created. pvc is the volume claim name, storageClass the missing class."},
	{types: []string{"Progressing"}, reason: "ImagePullFailed", english: "The game image cannot be pulled. Check the image reference.", description: "Progressing condition when the container image pull keeps failing."},
	{types: []string{"Progressing"}, reason: "CrashLoopBackOff", english: "The game container keeps crashing during startup ({restartCount} restarts so far). Check the logs.", description: "Progressing condition when the game container crash-loops. restartCount is the number of restarts."},
	{types: []string{"Progressing"}, reason: "ContainerExited", english: "The game container exited with code {exitCode} during startup. Check the logs.", description: "Progressing condition when the game container exits during startup. exitCode is the process exit code."},
	{types: []string{"Progressing"}, reason: "PullingImage", english: "Pulling the game image.", description: "Progressing detail while the container image downloads."},
	{types: []string{"Progressing"}, reason: "ContainerCreating", english: "Creating the container.", description: "Progressing detail while the kubelet creates the container."},
	{types: []string{"Progressing"}, reason: "WaitingForAgent", english: "The server is up. Waiting for the first heartbeat from the agent.", description: "Progressing detail once the game container runs but the agent has not reported yet."},
	{types: []string{"Progressing"}, reason: "InstallingServerFiles", english: "Downloading game files or waiting for the server to become ready.", description: "Progressing detail during first-run installation."},

	// GameServer: Healthy
	{types: []string{"Healthy"}, reason: "AgentFresh", english: "The agent is reporting heartbeats.", description: "Healthy condition while heartbeats arrive."},
	{types: []string{"Healthy"}, reason: "AgentStale", english: "The agent has not reported a heartbeat recently.", description: "Healthy condition when heartbeats stopped."},
	{types: []string{"Healthy"}, reason: "Pending", english: "The server has not started yet.", description: "Healthy condition before the first start."},
	{types: []string{"Healthy"}, reason: "Stopping", english: "The server is stopping.", description: "Healthy condition while stopping."},
	{types: []string{"Healthy"}, reason: "Suspended", english: "The server is stopped.", description: "Healthy condition for a stopped server."},
	{types: []string{"Healthy"}, reason: "IdleAsleep", english: "The server is asleep because no players are online.", description: "Healthy condition for an idle-sleeping server."},
	{types: []string{"Healthy"}, reason: "Failed", english: "The server failed.", description: "Healthy condition when the server has failed."},
	{types: []string{"Healthy"}, reason: "Unknown", english: "The state of the server is not known yet.", description: "Healthy condition before the operator has observed the server."},

	// GameServer: AddressAssignment ($REQ is the request phrase, see reqExpr)
	{types: []string{"AddressAssignment"}, reason: "IgnoredForExposureMode", english: "Requested $REQ is ignored: it applies only to the LoadBalancer expose mode, and this server is exposed as ''{expose}''.", description: "AddressAssignment condition when an address or pool is requested for a server that is not exposed as a LoadBalancer. expose is the current expose mode."},
	{types: []string{"AddressAssignment"}, reason: "InvalidAddress", english: "The requested address ''{address}'' is not a valid IP address.", description: "AddressAssignment condition when the requested address does not parse."},
	{types: []string{"AddressAssignment"}, reason: "NoAddressManagerConfigured", english: "Requested $REQ cannot be honored: this cluster has no load-balancer address manager configured, so any address comes from the default assignment policy.", description: "AddressAssignment condition when no address manager (MetalLB or Cilium) is configured."},
	{types: []string{"AddressAssignment"}, reason: "AddressInUse", english: "The requested address ''{address}'' is already in use by GameServer ''{server}''.", description: "AddressAssignment condition when another game server holds the requested address."},
	{types: []string{"AddressAssignment"}, reason: "PoolNotFound", english: "The requested address pool ''{pool}'' was not found.", description: "AddressAssignment condition when the requested address pool does not exist."},
	{types: []string{"AddressAssignment"}, reason: "AllocationFailed", english: "The address manager could not allocate an address: {detail}", description: "AddressAssignment condition when the address manager reports an allocation failure. detail is the address manager's own English message."},
	{types: []string{"AddressAssignment"}, reason: "ServiceNotReady", english: "Requested $REQ: waiting for the LoadBalancer Service that carries the request to exist.", description: "AddressAssignment condition before the load-balancer Service exists."},
	{types: []string{"AddressAssignment"}, reason: "AssignmentPending", english: "Requested $REQ: the address manager has not assigned an address yet.", description: "AddressAssignment condition while the address manager works."},
	{types: []string{"AddressAssignment"}, reason: "Assigned", english: "{assignedFrom, select, pool {Address {address} assigned from pool ''{pool}''.} other {Address {address} assigned.}}", description: "AddressAssignment condition once an address is assigned. assignedFrom is pool or default."},

	// GameServer: tunnel and other
	{types: []string{"TunnelReady"}, reason: "InvalidConfig", english: "{provider, select, none {The tunnel is enabled but the server has no tunnel configuration.} other {The ''{provider}'' tunnel provider is selected but its configuration is missing.}}", description: "TunnelReady condition for an incomplete tunnel setup. provider is the selected provider, or none."},
	{types: []string{"TunnelReady"}, reason: "DeploymentNotReady", english: "Waiting for the tunnel deployment to become ready.", description: "TunnelReady condition while the tunnel pod is not ready."},
	{types: []string{"TunnelReady"}, reason: "PortNotMapped", english: "No frp remote port is configured for: {ports}.", description: "TunnelReady condition for frp when advertised ports have no remote port. ports is a comma-separated list of port names."},
	{types: []string{"TunnelReady"}, reason: "NoCredentials", english: "No credentials secret was provided, so the ''{provider}'' tunnel cannot authenticate.", description: "TunnelReady condition when a tunnel provider needs credentials that are missing."},
	{types: []string{"TunnelReady"}, reason: "Ready", english: "{provider, select, playit {The tunnel deployment is ready. Waiting for the playit endpoint assignment.} tailscale {The tunnel deployment is ready and authenticated.} other {The tunnel deployment is ready and all ports are mapped.}}", description: "TunnelReady condition when the tunnel works."},
	{types: []string{"TunnelReady"}, reason: "UnknownProvider", english: "Unknown tunnel provider ''{provider}''.", description: "TunnelReady condition when the provider name is not supported."},
	{types: []string{"TunnelReady"}, reason: "TunnelCredentialRefused", english: "The tunnel credentials secret ''{secret}'' is not owned by this GameServer. {remedy, select, defaultName {Add an ownerReference to this GameServer to the Secret, or delete the Secret and save the credential again from the dashboard.} other {Save the credential from the dashboard, or add an ownerReference to this GameServer to the Secret.}}", description: "TunnelReady condition when the operator refuses to mount a credentials secret. secret is the secret name, remedy is defaultName or other."},
	{types: []string{"TunnelHostnameIgnored"}, reason: "SettingIgnored", english: "These settings apply to the backing Service, not to tunnel traffic: {settings}.", description: "Informational condition when hostname, source ranges or node ports are set on a server with a tunnel. settings is a comma-separated list of field names."},
	{types: []string{"TunnelAddressInvalid"}, reason: "ValidationFailed", english: "Some tunnel endpoints were rejected: {details}", description: "Condition when endpoints reported by the playit tunnel pod fail validation. details is the English validation text."},
	{types: []string{"IdleScheduleInvalid"}, reason: "WakeWindowUnparseable", english: "A wake window in the idle schedule cannot be parsed: {details}", description: "Condition when spec.idle has an invalid wake window. details is the English parse error."},
	{types: []string{"DataWipe"}, reason: "JobFailed", english: "The data wipe job did not complete. The volume may not be fully cleared.", description: "DataWipe condition when the wipe job fails."},

	// Backup, Restore, Unquiesced, SnapshotForgotten (Type Completed is shared)
	{types: []string{"Completed"}, reason: "Succeeded", english: "The backup or restore completed successfully.", description: "Completed condition on a Backup or Restore that finished."},
	{types: []string{"Completed"}, reason: "Failed", english: "The backup or restore failed.", description: "Completed condition on a Backup or Restore that failed. The message holds the English detail."},
	{types: []string{"Completed"}, reason: "JobFailed", english: "The backup job reported a failure.", description: "Completed condition on a Backup whose job failed."},
	{types: []string{"Completed"}, reason: "Pending", english: "The backup is waiting to start.", description: "Completed condition on a Backup that has not started (dynamic: reason is the phase).", dynamic: true},
	{types: []string{"Completed"}, reason: "Running", english: "The backup is running.", description: "Completed condition on a Backup in progress (dynamic: reason is the phase).", dynamic: true},
	{types: []string{"Unquiesced"}, reason: "Unquiesced", english: "The server was resumed after the backup.", description: "Unquiesced condition when the world was unfrozen."},
	{types: []string{"Unquiesced"}, reason: "Failed", english: "The server could not be resumed after the backup. Its world may still have auto-save turned off.", description: "Unquiesced condition when the unfreeze failed."},
	{types: []string{"SnapshotForgotten"}, reason: "Forgotten", english: "The snapshot was removed from the backup repository.", description: "SnapshotForgotten condition after a deleted Backup's snapshot was forgotten."},
	{types: []string{"SnapshotForgotten"}, reason: "ForgetAbandoned", english: "The snapshot was not removed from the backup repository when the backup was deleted.", description: "SnapshotForgotten condition when the forget step was abandoned."},

	// BackupSchedule
	{types: []string{"ScheduleValid"}, reason: "CronParseError", english: "The schedule is not a valid cron expression.", description: "ScheduleValid condition for an unparseable schedule."},
	{types: []string{"ScheduleValid"}, reason: "CronValid", english: "The schedule is a valid cron expression.", description: "ScheduleValid condition for a good schedule."},
	{types: []string{"RetentionTrimmed"}, reason: "TrimFailed", english: "Applying the retention policy failed.", description: "RetentionTrimmed condition when old backups could not be removed."},
	{types: []string{"RetentionTrimmed"}, reason: "TrimSucceeded", english: "The retention policy was applied.", description: "RetentionTrimmed condition after a successful trim."},

	// Cluster (Type Healthy; no overlap with the GameServer Healthy reasons)
	{types: []string{"Healthy"}, reason: "HealthCheckPassed", english: "The cluster is reachable and healthy.", description: "Healthy condition on a registered cluster."},
	{types: []string{"Healthy"}, reason: "NameReserved", english: "The cluster name \"local\" is reserved.", description: "Healthy condition when a cluster is registered with the reserved name."},
	{types: []string{"Healthy"}, reason: "SecretNotFound", english: "The kubeconfig secret was not found.", description: "Healthy condition when the kubeconfig secret is missing."},
	{types: []string{"Healthy"}, reason: "InvalidSecret", english: "The kubeconfig secret is missing the required label.", description: "Healthy condition when the secret lacks the cluster-kubeconfig label."},
	{types: []string{"Healthy"}, reason: "MissingKey", english: "The kubeconfig secret does not contain the expected key.", description: "Healthy condition when the kubeconfig key is absent."},
	{types: []string{"Healthy"}, reason: "BadKubeconfig", english: "The kubeconfig could not be used.", description: "Healthy condition when the kubeconfig does not parse."},
	{types: []string{"Healthy"}, reason: "DiscoveryClientFailed", english: "Connecting to the cluster failed.", description: "Healthy condition when the discovery client cannot be built."},
	{types: []string{"Healthy"}, reason: "HealthCheckFailed", english: "The cluster health check failed.", description: "Healthy condition when the cluster does not answer."},

	// Module (failure reasons are also written on Type Pulling)
	{types: []string{"Ready"}, reason: "Applied", english: "The module template was applied.", description: "Ready condition on a Module after its GameTemplate was written."},
	{types: []string{"Ready"}, reason: "InUse", english: "The module cannot be removed while game servers use its template.", description: "Ready condition when deletion is blocked."},
	{types: []string{"Ready", "Pulling"}, reason: "SourceNotFound", english: "The module source was not found.", description: "Module condition when the named ModuleSource does not exist."},
	{types: []string{"Ready", "Pulling"}, reason: "WaitingForCatalog", english: "Waiting for the module source to index this module.", description: "Module condition while the source has no catalog entry yet."},
	{types: []string{"Ready", "Pulling"}, reason: "NoVersionAvailable", english: "The module source has no available versions.", description: "Module condition when no version can be chosen."},
	{types: []string{"Ready", "Pulling"}, reason: "VersionUnavailable", english: "The requested version is not in the catalog.", description: "Module condition when the pinned version is missing."},
	{types: []string{"Ready", "Pulling"}, reason: "SourceConfig", english: "The module source configuration is invalid.", description: "Module condition for a broken source setup."},
	{types: []string{"Ready", "Pulling"}, reason: "PullFailed", english: "Pulling the module failed.", description: "Module condition when the OCI pull fails."},
	{types: []string{"Ready", "Pulling"}, reason: "VerifyConfig", english: "The signature verification settings are invalid.", description: "Module condition for a bad verification setup."},
	{types: []string{"Ready", "Pulling"}, reason: "SignatureInvalid", english: "The module signature could not be verified.", description: "Module condition when cosign verification fails."},
	{types: []string{"Ready", "Pulling"}, reason: "DigestMismatch", english: "The module content does not match its pinned digest.", description: "Module condition when the digest differs from the pin."},
	{types: []string{"Ready", "Pulling"}, reason: "IncompatibleOperator", english: "The module needs a newer Gameplane operator.", description: "Module condition when gameplaneMinVersion is not met."},
	{types: []string{"Ready", "Pulling"}, reason: "ApplyTemplate", english: "Applying the module template failed.", description: "Module condition when the GameTemplate cannot be written."},
	{types: []string{"Pulling"}, reason: "Pulling", english: "Pulling the module.", description: "Pulling condition while a pull runs."},
	{types: []string{"Pulling"}, reason: "Applied", english: "The module pull finished.", description: "Pulling condition once the template was applied."},
	{types: []string{"LocalesValid"}, reason: "Valid", english: "All module translations are valid.", description: "LocalesValid condition on a GameTemplate when every translation entry shipped with the module is valid."},
	{types: []string{"LocalesValid"}, reason: "InvalidEntries", english: "Some module translation entries are invalid and were left out.", description: "LocalesValid condition on a GameTemplate when translation entries or files were dropped. The message holds the English per-language counts."},
	{types: []string{"LocalesValid"}, reason: "LocalesTooLarge", english: "Some module translations were left out because they are too large.", description: "LocalesValid condition on a GameTemplate when a language file is over the size limit or the translations together exceed it. The message names the languages."},
	{types: []string{"LocalesValid"}, reason: "WriteFailed", english: "Module translations could not be stored.", description: "LocalesValid condition on a GameTemplate when the translations ConfigMap could not be written. The message holds the English error."},

	// ModuleSource
	{types: []string{"Synced"}, reason: "Indexed", english: "The module source was indexed.", description: "Synced condition after a successful index."},
	{types: []string{"Synced"}, reason: "IndexFailed", english: "Indexing the module source failed.", description: "Synced condition after a failed index."},
	{types: []string{"Ready"}, reason: "CatalogPopulated", english: "The module catalog is populated.", description: "Ready condition on a source with a catalog."},
	{types: []string{"Ready"}, reason: "SourceUnreachable", english: "The module source has never been indexed successfully.", description: "Ready condition on a source that cannot be reached."},
	{types: []string{"Ready"}, reason: "ServingStaleCatalog", english: "Serving the cached catalog. The last index attempt failed.", description: "Ready condition while an older catalog stays available."},

	// NetworkCapture
	{types: []string{"Failed"}, reason: "failed", english: "The capture failed.", description: "Failed condition on a NetworkCapture; the message holds the English detail."},
	{types: []string{"Failed"}, reason: "capture_already_in_progress", english: "Another capture is already running on this server.", description: "Failed condition when two captures target one server."},
	{types: []string{"Failed"}, reason: "PodRestarted", english: "The game pod restarted while the capture was running.", description: "Failed condition when the pod was replaced during a capture."},
	{types: []string{"Failed"}, reason: "SidecarCrashed", english: "The capture sidecar exited unexpectedly.", description: "Failed condition when the capture container died."},
	{types: []string{"SidecarStopFailed"}, reason: "sidecar_stop_failed", english: "Stopping the capture sidecar failed.", description: "Condition when the stop request to the sidecar failed."},
	{types: []string{"RetentionStopFailed"}, reason: "sidecar_stop_failed", english: "Stopping the capture sidecar at the end of retention failed.", description: "Condition when the retention-expiry stop failed."},
	{types: []string{"FileCleanupFailed"}, reason: "delete_failed", english: "Deleting the capture file failed. It may stay past its retention.", description: "Condition when the capture file could not be deleted."},
	{types: []string{"SidecarStopped"}, reason: "stopped", english: "The capture sidecar was told to stop.", description: "SidecarStopped condition after a stop request."},
	{types: []string{"SidecarStopped"}, reason: "never_started", english: "The capture was stopped before it started.", description: "SidecarStopped condition when a pending capture was cancelled."},
}

// entry is one generated pack entry with its condition id.
type entry struct {
	id    string
	value pack.Entry
}

func main() {
	app := flag.String("app", "../i18n/locales/en/app.json", "English reference app.json to update")
	controllers := flag.String("controllers", "internal/controller", "controller package directory scanned by the coverage guard")
	check := flag.Bool("check", false, "report drift against the table and exit 1 instead of writing")
	ids := flag.String("ids", "", "also write the generated condition.* ids to this file, one per line")
	flag.Parse()

	if err := run(*app, *controllers, *ids, *check); err != nil {
		fmt.Fprintln(os.Stderr, "condition-ids:", err)
		os.Exit(1)
	}
}

func run(appPath, controllersDir, idsPath string, check bool) error {
	entries, err := generate()
	if err != nil {
		return err
	}
	if err := guardReasons(controllersDir); err != nil {
		return err
	}
	if idsPath != "" {
		ids := make([]string, 0, len(entries))
		for _, e := range entries {
			ids = append(ids, e.id)
		}
		sort.Strings(ids)
		data := []byte(strings.Join(ids, "\n") + "\n")
		if err := os.WriteFile(filepath.Clean(idsPath), data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", idsPath, err)
		}
	}

	appPath = filepath.Clean(appPath)
	data, err := os.ReadFile(appPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", appPath, err)
	}
	existing, err := pack.ParseEntries(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", appPath, err)
	}

	if check {
		return checkDrift(existing, entries)
	}

	for id := range existing {
		if strings.HasPrefix(id, "condition.") {
			delete(existing, id)
		}
	}
	for _, e := range entries {
		existing[e.id] = e.value
	}
	out, err := pack.Marshal(existing)
	if err != nil {
		return err
	}
	if err := writeInDir(filepath.Dir(appPath), filepath.Base(appPath), out); err != nil {
		return fmt.Errorf("write %s: %w", appPath, err)
	}
	fmt.Printf("condition-ids: wrote %d condition.* ids to %s\n", len(entries), appPath)
	return nil
}

// generate expands the table into one entry per (type, reason) pair.
func generate() ([]entry, error) {
	var out []entry
	seen := map[string]bool{}
	for _, r := range rows {
		text := strings.ReplaceAll(r.english, "$REQ", reqExpr)
		for _, t := range r.types {
			id := "condition." + strings.ToLower(t) + "." + strings.ToLower(r.reason)
			if seen[id] {
				return nil, fmt.Errorf("duplicate id %s in table", id)
			}
			seen[id] = true
			out = append(out, entry{id: id, value: pack.Entry{DefaultMessage: text, Description: r.description}})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, nil
}

// checkDrift compares the condition.* ids in the English reference with the
// table. Any missing, extra or changed id is reported and returned as an error.
func checkDrift(existing map[string]pack.Entry, entries []entry) error {
	var problems []string
	want := map[string]bool{}
	for _, e := range entries {
		want[e.id] = true
		got, ok := existing[e.id]
		switch {
		case !ok:
			problems = append(problems, "missing "+e.id)
		case got.DefaultMessage != e.value.DefaultMessage || got.Description != e.value.Description:
			problems = append(problems, "changed "+e.id)
		}
	}
	for id := range existing {
		if strings.HasPrefix(id, "condition.") && !want[id] {
			problems = append(problems, "extra "+id)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("condition ids drift from the table (run make i18n-sync):\n  %s", strings.Join(problems, "\n  "))
}

// guardReasons parses the non-test controller sources and checks that the
// reason literals they set match the table's reason set in both directions.
func guardReasons(dir string) error {
	found, err := collectReasons(dir)
	if err != nil {
		return err
	}
	tableReasons := map[string]bool{}
	for _, r := range rows {
		tableReasons[r.reason] = true
	}

	var errs []error
	var unknown []string
	for reason, pos := range found {
		if !tableReasons[reason] {
			unknown = append(unknown, fmt.Sprintf("%s: reason %q has no condition table entry", pos, reason))
		}
	}
	sort.Strings(unknown)
	for _, u := range unknown {
		errs = append(errs, errors.New(u))
	}
	for _, r := range rows {
		if r.dynamic {
			continue
		}
		if _, ok := found[r.reason]; !ok {
			errs = append(errs, fmt.Errorf("table reason %q (%s) is not set by any controller file", r.reason, strings.Join(r.types, "|")))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return fmt.Errorf("coverage guard mismatch (report the diff, do not edit the table silently):\n  %s", strings.Join(msgs, "\n  "))
}

// collectReasons returns the first position of every non-empty reason literal
// the controllers can write.
func collectReasons(dir string) (map[string]string, error) {
	dir = filepath.Clean(dir)
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read controllers dir %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	found := map[string]string{}
	record := func(lit *ast.BasicLit, path string) {
		if lit == nil || lit.Kind != token.STRING {
			return
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil || v == "" {
			return
		}
		if _, ok := found[v]; !ok {
			found[v] = fmt.Sprintf("%s:%d", path, fset.Position(lit.Pos()).Line)
		}
	}
	for _, de := range dirEntries {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || guardSkipFiles[name] {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		collectFile(file, path, record)
	}
	return found, nil
}

// collectFile applies the guard's collection rules to one parsed file.
func collectFile(file *ast.File, path string, record func(*ast.BasicLit, string)) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			// Rule 4: the PVC provisioning reason constant.
			for _, spec := range d.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if name.Name == "GameServerConditionReasonPVCProvisioningFailed" && i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok {
							record(lit, path)
						}
					}
				}
			}
		case *ast.FuncDecl:
			// Rule 5: first string result of the pod-reason helpers.
			if d.Body != nil && guardReturnFuncs[d.Name.Name] {
				ast.Inspect(d.Body, func(n ast.Node) bool {
					ret, ok := n.(*ast.ReturnStmt)
					if !ok || len(ret.Results) == 0 {
						return true
					}
					if lit, ok := ret.Results[0].(*ast.BasicLit); ok {
						record(lit, path)
					}
					return true
				})
			}
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.KeyValueExpr:
			// Rule 1: composite-literal keys named Reason or reason.
			if key, ok := x.Key.(*ast.Ident); ok && (key.Name == "Reason" || key.Name == "reason") {
				if lit, ok := x.Value.(*ast.BasicLit); ok {
					record(lit, path)
				}
			}
		case *ast.AssignStmt:
			// Rule 2: .Reason / .reason / readyReason assigned a literal.
			if len(x.Lhs) != len(x.Rhs) {
				return true
			}
			for i, lhs := range x.Lhs {
				lit, ok := x.Rhs[i].(*ast.BasicLit)
				if !ok {
					continue
				}
				switch l := lhs.(type) {
				case *ast.SelectorExpr:
					if l.Sel.Name == "Reason" || l.Sel.Name == "reason" {
						record(lit, path)
					}
				case *ast.Ident:
					if l.Name == "readyReason" {
						record(lit, path)
					}
				}
			}
		case *ast.CallExpr:
			// Rule 3: the reason argument of the status helpers.
			name := ""
			switch f := x.Fun.(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				name = f.Sel.Name
			}
			idx, ok := guardCalls[name]
			if !ok || len(x.Args) <= idx {
				return true
			}
			if lit, ok := x.Args[idx].(*ast.BasicLit); ok {
				record(lit, path)
			}
		}
		return true
	})
}

// writeInDir writes name inside dir through an os.Root, so the write cannot
// escape dir.
func writeInDir(dir, name string, data []byte) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()
	if err := root.WriteFile(name, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}
