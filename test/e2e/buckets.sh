#!/usr/bin/env bash
# Single source of truth for the CI e2e test buckets.
#
# CI runs the Go e2e suite as several independent jobs, each executing one
# bucket via `go test -run "$(buckets.sh regex <name>)"`. Keeping the lists
# here (instead of inline in ci.yaml) lets `buckets.sh verify` prove the
# buckets are pairwise disjoint and exhaustive, so a new test can never be
# silently dropped from CI again.
#
# Usage:
#   buckets.sh buckets        print the bucket names
#   buckets.sh list <name>    print a bucket's test names, one per line
#   buckets.sh regex <name>   print the anchored ^(A|B|...)$ `go test -run` regex
#   buckets.sh verify         fail unless every test in test/e2e/*_test.go is
#                             in exactly one bucket (or explicitly unbucketed)
set -euo pipefail

cd "$(dirname "$0")"

# The regexes are anchored ^(...)$ — prefix matching (e.g. TestGameServer
# vs TestGameServer_Cascading) would silently run tests in more than one
# bucket.
#
# The buckets are cut by LOGIN PRESSURE, not by feature area. The API's
# login rate limiter is per-IP (5/min, burst 10) plus per-username
# (3/min, burst 6), and every test in a job shares one client IP through
# the kubectl port-forward — so admin logins, not CPU, bound an API
# bucket's wall clock. `operator` does zero logins and runs wide; each
# api bucket stays within its own cluster's login budget (~7 admin
# logins ≈ the per-user burst, so retries absorb the overflow instead
# of exhausting it). `ratelimit` deliberately drains the shared limiter,
# so it runs as a separate, LAST `go test` invocation (Go runs
# non-parallel tests first — inside the main bucket it would starve
# everyone else's logins).

bucket_operator() { cat <<'EOF'
TestHelmInstall_AllPodsReady
TestHelmInstall_AllCRDsPresent
TestHelmInstall_CRDApplyHookSkippedOnFreshInstall
TestHelmInstall_APIHealthz
TestHelmInstall_MetricsNotOnPublicPort
TestHelmInstall_APIServerEgressPolicy_AllowsPrivateRanges
TestHelmInstall_BringYourOwnMTLSCA
TestHelmInstall_MTLSClientCertReissuedOnCAChange
TestHelmInstall_APILogsClean
TestHelmInstall_OperatorLogsClean
TestCRD_Validation_GameServerWithoutTemplate
TestCRD_Validation_BackupScheduleBadCron
TestCRD_Validation_BackupRequiresServerRef
TestCRD_Validation_GameTemplateRequiresImage
TestCRD_Validation_IdleRejectsBadValues
TestCRD_Validation_TunnelServerAddrInvalid
TestCRD_Validation_TunnelProxyNameInvalid
TestCRD_Validation_TunnelServerAddrValid
TestGameServer_OperatorMaterializesChildren
TestGameServer_SuspendScalesToZeroAndBack
TestGameServer_IdleNeverSleepsWithoutAPlayerCount
TestGameServer_HeartbeatReachesRunning
TestGameServer_PVCSurvivesPodDelete
TestGameServer_InstallTimeDefaultApplies
TestGameServer_TemplateDefaultOverridesInstallTime
TestGameServer_ExplicitStorageClassOverridesDefault
TestGameServer_NonexistentStorageClassSurfacesError
TestGameServer_VersionSwitch
TestGameServer_WipeReportsFailureOnPermissionDenied
TestGameServer_CascadingDelete
TestGameServer_TunnelCredentialRefusalSurfaced
TestGameServer_IngressNetworkPolicyShapeAndCascade
TestGameServer_NetworkCaptureStartStopDownload
TestGameServer_NetworkCaptureEphemeralContainer
TestGameServer_NetworkCaptureRestartCleanup
TestGameServer_NetworkCaptureConcurrencyRejected
TestNetworkCapture_RetentionExpiry
TestGameTemplate_DeletionWithLiveServer
TestModule_FinalizerBlocksWhileTemplateInUse
TestModule_FailsOnUnreachableRegistry
TestModule_VerifyRejectsUnsignedBundle
TestModule_VerifySignedBundleInstalls
TestModule_DigestPinEnforcedAfterInstall
TestModuleSource_RejectsSSRFTarget
TestModuleSourceAndModule
TestModuleSourceUpload
TestModule_ScaffoldAndPackage
TestModule_SteamcmdScaffoldKeepsNonRootSecurity
TestBackup_OperatorMaterializesJob
TestBackup_FailsOnMissingPVC
TestBackup_FailsOnBadCredentials
TestBackup_DeleteForgetsSnapshot
TestBackupSchedule_CreatesBackupCR
TestBackupSchedule_SuspendStopsScheduling
TestBackupSchedule_RetentionTrimsPast
TestBackupSchedule_ConcurrencyForbid
TestRestore_RoundTrip
TestRestore_RejectsMissingBackup
TestRestore_FailsOnMissingSnapshot
TestGameServer_UnownedNamesakeSurvives
TestAddressPool_DefaultPoolNoPreference
TestAddressPool_NamedPoolAssignment
TestAddressPool_NonLoadBalancerIgnored
TestAddressPool_NonexistentPoolError
TestAddressPool_ChangePoolOnRunningServer
TestAddressPool_RESTAPICarriesPool
TestAddressPool_StatusVisibleInAddressAssignmentCondition
TestAddressPool_ExplicitAddressRequest
TestAddressPool_AddressInUseConflict
TestAddressPool_PoolExhausted
TestAPI_EventStreamAndRoleEdits_FollowCallerPermissions
EOF
}

# TestAPI_EventStreamAndRoleEdits_FollowCallerPermissions sits in operator for
# the login budget, not by subject: api-roles is at its ceiling and api-rbac is
# over it. It costs +1 e2e-admin login plus one login as its own user.

bucket_api_auth() { cat <<'EOF'
TestAPI_BootstrapAndLogin
TestAPI_LoginPrivacy
TestAPI_DynamicAuthProviders
TestAPI_AuditEmitsOnMutation
TestAPI_AuditPaginationAndFilter
TestAPI_LogoutInvalidatesSession
TestAPI_PasswordResetInvalidatesSession
TestAPI_OIDCHelmSeeded_AdminOnFirstLogin
TestAPI_OIDCHelmSeeded_DefaultRoleOnNoMapping
TestAPI_OIDCHelmSeeded_RoleReevaluatedOnGroupChange
EOF
}

# The three TestAPI_OIDCHelmSeeded_* tests above spend 0 of this bucket's
# admin-login budget: they authenticate entirely as OIDC users against a
# fake in-cluster IdP (test/e2e/internal/fakeoidc), never as e2e-admin,
# and their one rate limiter (auth.OIDCCallbackLimiter, burst 10/min) is
# untouched by every other test in this bucket. See T049.
#
# WARNING: Do NOT move any test that writes helmOverride.roleMappings into
# api-auth — it overrides the Helm-seeded admin mapping at login time and
# would flake TestAPI_OIDCHelmSeeded_*.

# TestAPI_AuthConfig_RoleMappings belongs with auth by subject, but api-auth was
# already at its admin-login ceiling (per-user burst 6, 3/min, one shared IP per
# job) and a ninth login there made TestAPI_LogoutInvalidatesSession fail 429.
# It lives here, in a bucket that spent 4 admin logins before the tests below
# were added (see the running tally further down).
#
# WARNING: Do NOT move any test that writes helmOverride.roleMappings into
# api-auth — it overrides the Helm-seeded admin mapping at login time and
# would flake TestAPI_OIDCHelmSeeded_*.
#
# TestAPI_OIDCHelmOverride_EffectiveAtLoginTime writes helmOverride.roleMappings
# and must not share an api-auth bucket with TestAPI_OIDCHelmSeeded_* tests
# (separate CI job/cluster per bucket, so no conflict, but maintains login-budget discipline).
# It costs +1 admin login, bringing api-roles to 6 (up from the previous 5, after
# TestAPI_AuthConfig_RoleMappings was added above).
# Budget remains within the ~7 admin-logins-per-job ceiling.
#
# TestAPI_ThemePreferences (user theme preferences, contracts/user-preferences-api.md)
# lands here because api-auth is at its admin-login ceiling and this bucket had
# headroom when it was added (before the ceiling note below): it costs +1
# e2e-admin login (to create its own viewer user) plus one login under a fresh
# UnixNano username, bringing api-roles to 7. It writes only its own user's
# preference rows — no auth config, no roleMappings — so it cannot interfere
# with the OIDC/role-mapping tests above.
#
# api-roles is now at 7 admin logins — the documented ~7/job ceiling — with no
# headroom left. A new admin-login test does not belong in this bucket; route
# it elsewhere or add a new bucket instead.
bucket_api_roles() { cat <<'EOF'
TestAPI_CustomRole_Lifecycle
TestAPI_BuiltinRole_Immutable
TestAPI_PerNamespaceBinding_GrantsScopedAccess
TestAPI_OwnerCollaboratorAccess
TestAPI_AuthConfig_RoleMappings
TestAPI_OIDCHelmOverride_EffectiveAtLoginTime
TestAPI_ThemePreferences
EOF
}

bucket_api_rbac() { cat <<'EOF'
TestAPI_RBAC_ViewerCannotMutate
TestAPI_RBAC_ViewerCannotMutate_Matrix
TestAPI_RBAC_OperatorCanWriteServers_NotUsers
TestAPI_RBAC_OperatorCannotReachCaptures
TestAPI_RBAC_AdminCanReachAll
TestAPI_OperatorCannotInviteUsers
TestAPI_LifecycleClone
TestAPI_LifecycleNotFound
EOF
}

bucket_api_agent() { cat <<'EOF'
TestAPI_AgentFilesRoundTrip
TestAPI_AgentPlayers
TestAPI_AgentUnreachable
TestAPI_ConsolePTYRoundTrip
TestAPI_LogsTailWS
TestAPI_LifecycleStartStop
TestAPI_LifecycleRestart
EOF
}

# TestAPI_OwnerOnlyServerOperations_RequireOwnerOrAdmin lives here for the
# login budget, not by subject: api-roles is at its ceiling. It costs +1
# e2e-admin login (bringing api-mods to 6) plus one login as its own
# operator-role user.
# TestAPI_BootstrapAdminForceEndsExistingSessions now costs +1 e2e-admin
# login (for cleanup) plus one login as its own throwaway account (a fresh
# per-username bucket, one slot of the job's shared per-IP budget). This
# brings api-mods to 7.
bucket_api_mods() { cat <<'EOF'
TestAPI_ModManifestInstallUpgrade
TestAPI_ModUpload
TestAPI_ModArchiveConfinement_PathTraversalRejected
TestAPI_ModArchiveConfinement_SymlinkEscapeRejected
TestAPI_ModArchiveConfinement_ValidArchiveExtracts
TestAPI_OwnerOnlyServerOperations_RequireOwnerOrAdmin
TestAPI_BootstrapAdminForceEndsExistingSessions
EOF
}

bucket_ratelimit() { cat <<'EOF'
TestAPI_LoginRateLimit
EOF
}

# bot-fast is a DIFFERENT set from fastGameSet (gamebot_helpers_e2e_test.go).
# fastGameSet is the default game scope for a manual/local
# GAMEPLANE_E2E_GAME_BOT=1 run with GAMEPLANE_E2E_GAMES unset: minecraft-java,
# terraria, factorio, garrys-mod, tmodloader, beammp (six games). bot-fast is
# the narrower CI bucket below: only three of those six games (minecraft-java,
# terraria, garrys-mod) plus the wake-on-connect tests.
# factorio, tmodloader and beammp are in fastGameSet but are bucketed under
# bot-heavy instead, mainly to stay within the GitHub runner's disk budget in
# CI — see the bot-heavy comments below for the per-game rationale.
bucket_bot_fast() { cat <<'EOF'
TestGameServer_MinecraftJavaBot_Joined
TestGameServer_TerrariaBot_Joined
TestGameServer_GarrysModBot_Query
TestGameServer_WakeOnConnect_PingDoesNotWake
TestGameServer_WakeOnConnect_LoginWakes
TestGameServer_WakeOnConnect_UnarmedNoSentinel
EOF
}

# Games with heavy resource requirements, excluded from default CI execution.
# This bucket is DELIBERATELY never run in CI — not on PRs, not nightly, not
# on `schedule:`, not via `workflow_dispatch:`. It is validated only by a
# maintainer hand-run with `GAMEPLANE_E2E_GAMES=all`. Do not add CI jobs or
# workflows that execute this bucket; that would defeat its purpose.
#
# IMPORTANT: A test being bucketed (listed here) is NOT evidence it runs in
# CI. This bucket is bucketed for discovery and audit purposes but is never
# executed by the default job. The distinction between "bucketed" and
# "executed in CI" is load-bearing — it prevents silent test loss while
# acknowledging resource constraints that prevent CI execution.
# Why each of these is excluded from default CI execution (FR-003):
#   TestGameServer_ArkBot_Query: 30Gi persistent storage + multi-GB container image
#   TestGameServer_CS2Bot_Query: 60Gi storage requirement (image docs minimum); GitHub runner ~14GB disk insufficient
#   TestGameServer_DayZBot_Query: 40Gi storage + ~10GB+ SteamCMD re-download on each restart
#   TestGameServer_DontStarveTogetherBot_Query: Modest alone (5Gi storage, 1Gi memory) but cumulative with heavier games
#   TestGameServer_EnshroudedBot_Query: 25Gi storage + sustained memory (8Gi requests, 16Gi limits)
#   TestGameServer_FactorioBot_Query: Modest alone (5Gi storage, 1Gi memory) but cumulative with heavier games
#   TestGameServer_PalworldBot_Query: 20Gi storage + multi-GB SteamCMD download on first boot
#   TestGameServer_ProjectZomboidBot_Query: 15Gi storage + 4Gi memory requests
#   TestGameServer_RustBot_Query: 10Gi storage + 4Gi memory requests
#   TestGameServer_SatisfactoryBot_Query: 25Gi storage + multi-GB SteamCMD download on first boot
#   TestGameServer_SevenDaysToDieBot_Query: 55Gi combined storage (10Gi saves + 45Gi install) — largest single footprint
#   TestGameServer_ValheimBot_Query: 5Gi storage but ~12GB SteamCMD download on first boot (exceeds runner disk)
#   TestGameServer_VRisingBot_Query: 20Gi storage + Wine/Ubuntu base image overhead
#   TestGameServer_FiveM_Persistence: 10Gi storage + embedded txAdmin database (exceeds runner disk)
#   TestGameServer_ArkSurvival_ClusterPersistence: 35Gi storage + cluster-travel multi-shard footprint
#   TestGameServer_TeamFortress2_RCON: 15Gi storage + SteamCMD download (exceeds runner disk)
#   TestGameServer_Squad_RCON: 40Gi storage + SteamCMD download (exceeds runner disk)
#   TestGameServer_TModLoader_Mods: Multi-GB tModLoader/.NET image download and persistent mod storage
#   TestGameServer_DayZ_Workshop: 40Gi storage + SteamCMD workshop download (exceeds runner disk)
#   TestGameServer_ArkSurvivalEvolvedBot_Query: 30Gi storage + SteamCMD download (exceeds runner disk)
#   TestGameServer_ArmaReforgerBot_Query: 25Gi storage + SteamCMD download (exceeds runner disk)
#   TestGameServer_BeamMPBot_Query: 5Gi storage + vehicle assets overhead (exceeds runner disk)
#   TestGameServer_EuroTruckSimulator2Bot_Query: 5Gi storage + SteamCMD download (exceeds runner disk)
#   TestGameServer_FarmingSimulator25Bot_Query: 20Gi storage + Wine/Xvfb overhead (exceeds runner disk)
#   TestGameServer_FiveMBot_Query: 10Gi storage + embedded txAdmin database (exceeds runner disk)
#   TestGameServer_HellLetLooseBot_Query: 30Gi storage + SteamCMD download (exceeds runner disk)
#   TestGameServer_Left4Dead2Bot_Query: 15Gi storage + SteamCMD download (exceeds runner disk)
#   TestGameServer_MountAndBlade2BannerlordBot_Query: 15Gi storage + SteamCMD download (exceeds runner disk)
#   TestGameServer_SquadBot_Query: 40Gi storage + SteamCMD download (exceeds runner disk)
#   TestGameServer_TeamFortress2Bot_Query: 15Gi storage + SteamCMD download (exceeds runner disk)
#   TestGameServer_TheIsleBot_Query: 20Gi storage + UE4 + SteamCMD download (exceeds runner disk)
#   TestGameServer_TModLoaderBot_Query: 5Gi storage + .NET/SteamCMD download (exceeds runner disk)
bucket_bot_heavy() { cat <<'EOF'
TestGameServer_ArkBot_Query
TestGameServer_ArkSurvivalEvolvedBot_Query
TestGameServer_ArkSurvival_ClusterPersistence
TestGameServer_ArmaReforgerBot_Query
TestGameServer_BeamMPBot_Query
TestGameServer_CS2Bot_Query
TestGameServer_DayZBot_Query
TestGameServer_DayZ_Workshop
TestGameServer_DontStarveTogetherBot_Query
TestGameServer_EnshroudedBot_Query
TestGameServer_EuroTruckSimulator2Bot_Query
TestGameServer_FactorioBot_Query
TestGameServer_FarmingSimulator25Bot_Query
TestGameServer_FiveMBot_Query
TestGameServer_FiveM_Persistence
TestGameServer_HellLetLooseBot_Query
TestGameServer_Left4Dead2Bot_Query
TestGameServer_MountAndBlade2BannerlordBot_Query
TestGameServer_PalworldBot_Query
TestGameServer_ProjectZomboidBot_Query
TestGameServer_RustBot_Query
TestGameServer_SatisfactoryBot_Query
TestGameServer_SevenDaysToDieBot_Query
TestGameServer_SquadBot_Query
TestGameServer_Squad_RCON
TestGameServer_TeamFortress2Bot_Query
TestGameServer_TeamFortress2_RCON
TestGameServer_TheIsleBot_Query
TestGameServer_TModLoaderBot_Query
TestGameServer_TModLoader_Mods
TestGameServer_ValheimBot_Query
TestGameServer_VRisingBot_Query
EOF
}

# Needs a SECOND kind cluster (dual-cluster ?cluster= dispatch + scoped
# RBAC), which none of the other buckets' single-cluster jobs provide — see
# multicluster_e2e_test.go's package doc. Its own dedicated CI job
# (e2e-multicluster) brings up both clusters before running it.
#
# TestAPI_AccountRemoval_RevokesSharesAndAllowsSSOReprovision and
# TestAPI_ModuleUpload_ExtractionStaysWithinBudget live here for the login
# budget, not by subject: api-mods is at its ~7 e2e-admin-login ceiling. Each
# costs +1 e2e-admin login (account removal also uses fake-OIDC sign-ins,
# which every e2e.sh cluster deploys), bringing this job to 3; both run
# against the primary cluster.
bucket_multicluster() { cat <<'EOF'
TestAPI_AccountRemoval_RevokesSharesAndAllowsSSOReprovision
TestAPI_ModuleUpload_ExtractionStaysWithinBudget
TestMultiCluster_ClusterDispatchAndScopedRBAC
TestMultiCluster_GatewayParity
EOF
}

# Needs a cluster seeded with the PREVIOUS RELEASE's published chart and GHCR
# images (deploy/kind/upgrade.sh), which no other bucket's job provides — they
# all install the working-tree chart. The test then helm-upgrades that release
# to the working tree, so it also cannot share a cluster with anything else.
# Its own dedicated CI job (e2e-upgrade) sets this up.
bucket_upgrade() { cat <<'EOF'
TestUpgrade_FromPreviousRelease
EOF
}

# One sequential lifecycle test that runs `helm upgrade --reuse-values` (1m
# report interval, dashboard token, public summary, no per-source ingest
# limit) on its own bucket cluster, so it must not share a cluster with other
# tests. It makes ONE admin login (budget: 2 or fewer). Spec 022 research R17.
bucket_telemetry() { cat <<'EOF'
TestTelemetryLifecycle
EOF
}

# Tests that exist in the suite but deliberately run in NO bucket. Every
# entry needs a reason; `verify` fails on any unlisted stray so additions
# here are a conscious, reviewed act. Currently empty.
unbucketed() { :; }

bucket_names() {
	printf '%s\n' operator api-auth api-roles api-rbac api-agent api-mods ratelimit bot-fast bot-heavy multicluster upgrade telemetry
}

list_bucket() {
	case "$1" in
	operator) bucket_operator ;;
	api-auth) bucket_api_auth ;;
	api-roles) bucket_api_roles ;;
	api-rbac) bucket_api_rbac ;;
	api-agent) bucket_api_agent ;;
	api-mods) bucket_api_mods ;;
	ratelimit) bucket_ratelimit ;;
	bot-fast) bucket_bot_fast ;;
	bot-heavy) bucket_bot_heavy ;;
	multicluster) bucket_multicluster ;;
	upgrade) bucket_upgrade ;;
	telemetry) bucket_telemetry ;;
	*)
		echo "unknown bucket: $1 (known: $(bucket_names | tr '\n' ' '))" >&2
		exit 2
		;;
	esac
}

regex_bucket() {
	printf '^(%s)$\n' "$(list_bucket "$1" | paste -sd'|' -)"
}

suite_tests() {
	# Every e2e test lives flat in this directory behind the same
	# `//go:build e2e` tag, so a file grep is an exact inventory and
	# needs no Go toolchain. TestMain is the harness, not a test.
	grep -hoE '^func Test[A-Za-z0-9_]+' ./*_test.go |
		sed 's/^func //' |
		grep -vx 'TestMain' |
		sort
}

verify() {
	local suite union dups missing stray fail=0
	suite=$(suite_tests)
	union=$( (bucket_names | while read -r b; do list_bucket "$b"; done; unbucketed) | sort)

	dups=$(printf '%s\n' "$union" | uniq -d)
	if [ -n "$dups" ]; then
		echo "FAIL: listed in more than one bucket:" >&2
		printf '%s\n' "$dups" >&2
		fail=1
	fi

	missing=$(comm -23 <(printf '%s\n' "$suite") <(printf '%s\n' "$union" | uniq))
	if [ -n "$missing" ]; then
		echo "FAIL: in the suite but in no bucket (add to a bucket in $0):" >&2
		printf '%s\n' "$missing" >&2
		fail=1
	fi

	stray=$(comm -13 <(printf '%s\n' "$suite") <(printf '%s\n' "$union" | uniq))
	if [ -n "$stray" ]; then
		echo "FAIL: bucketed but not found in the suite (deleted or renamed test?):" >&2
		printf '%s\n' "$stray" >&2
		fail=1
	fi

	if [ "$fail" -ne 0 ]; then
		exit 1
	fi
	echo "buckets OK: $(printf '%s\n' "$suite" | grep -c .) tests, all in exactly one bucket"
}

case "${1:-}" in
buckets) bucket_names ;;
list) list_bucket "${2:?usage: buckets.sh list <bucket>}" ;;
regex) regex_bucket "${2:?usage: buckets.sh regex <bucket>}" ;;
verify) verify ;;
*)
	sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
	;;
esac
