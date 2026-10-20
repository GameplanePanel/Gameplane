# E2E test to CI bucket map (T004)

Source: `test/e2e/*_test.go` (top level only, 157 `func Test` declarations). Bucket source: `test/e2e/buckets.sh`.

**How buckets.sh maps tests:** by explicit name lists, not by file or name pattern. Each `bucket_<name>()` function prints one test name per line from a heredoc (e.g. `bucket_operator()` at `buckets.sh:36`). `regex_bucket` joins the names into an anchored `^(A|B|...)$` regex for `go test -run` (`buckets.sh:403`). Anchoring exists so prefix matches do not run a test in two buckets (`buckets.sh:20`). The file a test lives in plays no role in bucketing.

**Verification done by reading (not running):** 156 test names in the bucket lists, 156 `func Test` declarations excluding `TestMain`. Every bucketed name matches a suite function, every suite function appears in exactly one bucket, and no name is listed twice.

## Table

Bucket column gives the bucket name and the line of its entry in `buckets.sh`.

| Test name | File:line | Bucket(s) |
|---|---|---|
| `TestAPI_AccountRemoval_RevokesSharesAndAllowsSSOReprovision` | `test/e2e/api_account_removal_e2e_test.go:121` | multicluster (buckets.sh:336) |
| `TestAPI_AgentFilesRoundTrip` | `test/e2e/api_agent_e2e_test.go:92` | api-agent (buckets.sh:191) |
| `TestAPI_AgentPlayers` | `test/e2e/api_agent_e2e_test.go:389` | api-agent (buckets.sh:192) |
| `TestAPI_AgentUnreachable` | `test/e2e/api_agent_e2e_test.go:429` | api-agent (buckets.sh:193) |
| `TestAPI_BootstrapAndLogin` | `test/e2e/api_auth_e2e_test.go:32` | api-auth (buckets.sh:116) |
| `TestAPI_LoginPrivacy` | `test/e2e/api_auth_e2e_test.go:67` | api-auth (buckets.sh:117) |
| `TestAPI_RBAC_ViewerCannotMutate` | `test/e2e/api_auth_e2e_test.go:119` | api-rbac (buckets.sh:179) |
| `TestAPI_AuditEmitsOnMutation` | `test/e2e/api_auth_e2e_test.go:172` | api-auth (buckets.sh:119) |
| `TestAPI_DynamicAuthProviders` | `test/e2e/api_auth_e2e_test.go:240` | api-auth (buckets.sh:118) |
| `TestAPI_AuthConfig_RoleMappings` | `test/e2e/api_auth_e2e_test.go:412` | api-roles (buckets.sh:172) |
| `TestAPI_OIDCHelmSeeded_AdminOnFirstLogin` | `test/e2e/api_auth_e2e_test.go:903` | api-auth (buckets.sh:123) |
| `TestAPI_OIDCHelmSeeded_DefaultRoleOnNoMapping` | `test/e2e/api_auth_e2e_test.go:920` | api-auth (buckets.sh:124) |
| `TestAPI_OIDCHelmSeeded_RoleReevaluatedOnGroupChange` | `test/e2e/api_auth_e2e_test.go:941` | api-auth (buckets.sh:125) |
| `TestAPI_OIDCHelmOverride_EffectiveAtLoginTime` | `test/e2e/api_auth_e2e_test.go:969` | api-roles (buckets.sh:173) |
| `TestAPI_BootstrapAdminForceEndsExistingSessions` | `test/e2e/api_breakglass_e2e_test.go:23` | api-mods (buckets.sh:216) |
| `TestAPI_EventStreamAndRoleEdits_FollowCallerPermissions` | `test/e2e/api_events_roles_scope_e2e_test.go:23` | operator (buckets.sh:107) |
| `TestAPI_LifecycleStartStop` | `test/e2e/api_lifecycle_e2e_test.go:25` | api-agent (buckets.sh:196) |
| `TestAPI_LifecycleRestart` | `test/e2e/api_lifecycle_e2e_test.go:105` | api-agent (buckets.sh:197) |
| `TestAPI_LifecycleClone` | `test/e2e/api_lifecycle_e2e_test.go:173` | api-rbac (buckets.sh:185) |
| `TestAPI_LifecycleNotFound` | `test/e2e/api_lifecycle_e2e_test.go:258` | api-rbac (buckets.sh:186) |
| `TestAPI_ModArchiveConfinement_PathTraversalRejected` | `test/e2e/api_mods_confinement_e2e_test.go:70` | api-mods (buckets.sh:212) |
| `TestAPI_ModArchiveConfinement_SymlinkEscapeRejected` | `test/e2e/api_mods_confinement_e2e_test.go:176` | api-mods (buckets.sh:213) |
| `TestAPI_ModArchiveConfinement_ValidArchiveExtracts` | `test/e2e/api_mods_confinement_e2e_test.go:290` | api-mods (buckets.sh:214) |
| `TestAPI_ModManifestInstallUpgrade` | `test/e2e/api_mods_e2e_test.go:101` | api-mods (buckets.sh:210) |
| `TestAPI_ModUpload` | `test/e2e/api_mods_upload_e2e_test.go:21` | api-mods (buckets.sh:211) |
| `TestAPI_ModuleUpload_ExtractionStaysWithinBudget` | `test/e2e/api_module_upload_budget_e2e_test.go:82` | multicluster (buckets.sh:337) |
| `TestAPI_OwnerCollaboratorAccess` | `test/e2e/api_owner_collab_e2e_test.go:34` | api-roles (buckets.sh:171) |
| `TestAPI_OwnerOnlyServerOperations_RequireOwnerOrAdmin` | `test/e2e/api_owner_only_ops_e2e_test.go:26` | api-mods (buckets.sh:215) |
| `TestAPI_RBAC_ViewerCannotMutate_Matrix` | `test/e2e/api_rbac_matrix_e2e_test.go:20` | api-rbac (buckets.sh:180) |
| `TestAPI_RBAC_OperatorCanWriteServers_NotUsers` | `test/e2e/api_rbac_matrix_e2e_test.go:107` | api-rbac (buckets.sh:181) |
| `TestAPI_OperatorCannotInviteUsers` | `test/e2e/api_rbac_matrix_e2e_test.go:196` | api-rbac (buckets.sh:184) |
| `TestAPI_RBAC_OperatorCannotReachCaptures` | `test/e2e/api_rbac_matrix_e2e_test.go:240` | api-rbac (buckets.sh:182) |
| `TestAPI_RBAC_AdminCanReachAll` | `test/e2e/api_rbac_matrix_e2e_test.go:296` | api-rbac (buckets.sh:183) |
| `TestAPI_CustomRole_Lifecycle` | `test/e2e/api_roles_e2e_test.go:19` | api-roles (buckets.sh:168) |
| `TestAPI_BuiltinRole_Immutable` | `test/e2e/api_roles_e2e_test.go:120` | api-roles (buckets.sh:169) |
| `TestAPI_PerNamespaceBinding_GrantsScopedAccess` | `test/e2e/api_roles_e2e_test.go:146` | api-roles (buckets.sh:170) |
| `TestAPI_PasswordResetInvalidatesSession` | `test/e2e/api_session_e2e_test.go:22` | api-auth (buckets.sh:122) |
| `TestAPI_LoginRateLimit` | `test/e2e/api_session_e2e_test.go:95` | ratelimit (buckets.sh:221) |
| `TestAPI_AuditPaginationAndFilter` | `test/e2e/api_session_e2e_test.go:155` | api-auth (buckets.sh:120) |
| `TestAPI_LogoutInvalidatesSession` | `test/e2e/api_session_e2e_test.go:212` | api-auth (buckets.sh:121) |
| `TestAPI_ThemePreferences` | `test/e2e/api_theme_preferences_e2e_test.go:140` | api-roles (buckets.sh:174) |
| `TestAPI_ConsolePTYRoundTrip` | `test/e2e/api_ws_e2e_test.go:33` | api-agent (buckets.sh:194) |
| `TestAPI_LogsTailWS` | `test/e2e/api_ws_e2e_test.go:123` | api-agent (buckets.sh:195) |
| `TestGameServer_ArkBot_Query` | `test/e2e/ark_bot_e2e_test.go:27` | bot-heavy (buckets.sh:289) |
| `TestGameServer_ArkSurvival_ClusterPersistence` | `test/e2e/arksurvival_cluster_persistence_e2e_test.go:25` | bot-heavy (buckets.sh:291) |
| `TestGameServer_ArkSurvivalEvolvedBot_Query` | `test/e2e/arksurvivalevolved_bot_e2e_test.go:20` | bot-heavy (buckets.sh:290) |
| `TestGameServer_ArmaReforgerBot_Query` | `test/e2e/armareforger_bot_e2e_test.go:20` | bot-heavy (buckets.sh:292) |
| `TestBackup_OperatorMaterializesJob` | `test/e2e/backup_e2e_test.go:27` | operator (buckets.sh:85) |
| `TestBackupSchedule_CreatesBackupCR` | `test/e2e/backup_e2e_test.go:122` | operator (buckets.sh:89) |
| `TestBackup_DeleteForgetsSnapshot` | `test/e2e/backup_forget_e2e_test.go:29` | operator (buckets.sh:88) |
| `TestBackupSchedule_SuspendStopsScheduling` | `test/e2e/backupschedule_e2e_test.go:23` | operator (buckets.sh:90) |
| `TestBackupSchedule_RetentionTrimsPast` | `test/e2e/backupschedule_e2e_test.go:103` | operator (buckets.sh:91) |
| `TestBackupSchedule_ConcurrencyForbid` | `test/e2e/backupschedule_e2e_test.go:223` | operator (buckets.sh:92) |
| `TestGameServer_BeamMPBot_Query` | `test/e2e/beammp_bot_e2e_test.go:20` | bot-heavy (buckets.sh:293) |
| `TestGameServer_CaptureOverhead_Joined` | `test/e2e/capture_overhead_e2e_test.go:133` | capture-overhead (buckets.sh:368) |
| `TestCRD_Validation_GameServerWithoutTemplate` | `test/e2e/crd_validation_e2e_test.go:18` | operator (buckets.sh:47) |
| `TestCRD_Validation_BackupScheduleBadCron` | `test/e2e/crd_validation_e2e_test.go:33` | operator (buckets.sh:48) |
| `TestCRD_Validation_BackupRequiresServerRef` | `test/e2e/crd_validation_e2e_test.go:52` | operator (buckets.sh:49) |
| `TestCRD_Validation_GameTemplateRequiresImage` | `test/e2e/crd_validation_e2e_test.go:68` | operator (buckets.sh:50) |
| `TestCRD_Validation_TunnelServerAddrInvalid` | `test/e2e/crd_validation_e2e_test.go:109` | operator (buckets.sh:52) |
| `TestCRD_Validation_TunnelProxyNameInvalid` | `test/e2e/crd_validation_e2e_test.go:134` | operator (buckets.sh:53) |
| `TestCRD_Validation_TunnelServerAddrValid` | `test/e2e/crd_validation_e2e_test.go:159` | operator (buckets.sh:54) |
| `TestGameServer_CS2Bot_Query` | `test/e2e/cs2_bot_e2e_test.go:35` | bot-heavy (buckets.sh:294) |
| `TestGameServer_DayZBot_Query` | `test/e2e/dayz_bot_e2e_test.go:33` | bot-heavy (buckets.sh:295) |
| `TestGameServer_DayZ_Workshop` | `test/e2e/dayz_workshop_e2e_test.go:25` | bot-heavy (buckets.sh:296) |
| `TestGameServer_DontStarveTogetherBot_Query` | `test/e2e/dontstarve_bot_e2e_test.go:37` | bot-heavy (buckets.sh:297) |
| `TestGameServer_EnshroudedBot_Query` | `test/e2e/enshrouded_bot_e2e_test.go:30` | bot-heavy (buckets.sh:298) |
| `TestGameServer_EuroTruckSimulator2Bot_Query` | `test/e2e/eurotrucksimulator2_bot_e2e_test.go:20` | bot-heavy (buckets.sh:299) |
| `TestGameServer_FactorioBot_Query` | `test/e2e/factorio_bot_e2e_test.go:34` | bot-heavy (buckets.sh:300) |
| `TestBackup_FailsOnMissingPVC` | `test/e2e/failure_paths_e2e_test.go:21` | operator (buckets.sh:86) |
| `TestBackup_FailsOnBadCredentials` | `test/e2e/failure_paths_e2e_test.go:77` | operator (buckets.sh:87) |
| `TestRestore_FailsOnMissingSnapshot` | `test/e2e/failure_paths_e2e_test.go:176` | operator (buckets.sh:95) |
| `TestModule_FailsOnUnreachableRegistry` | `test/e2e/failure_paths_e2e_test.go:256` | operator (buckets.sh:76) |
| `TestGameServer_FarmingSimulator25Bot_Query` | `test/e2e/farmingsimulator25_bot_e2e_test.go:20` | bot-heavy (buckets.sh:301) |
| `TestGameServer_FiveMBot_Query` | `test/e2e/fivem_bot_e2e_test.go:20` | bot-heavy (buckets.sh:302) |
| `TestGameServer_FiveM_Persistence` | `test/e2e/fivem_persistence_e2e_test.go:23` | bot-heavy (buckets.sh:303) |
| `TestGameServer_OperatorMaterializesChildren` | `test/e2e/gameserver_e2e_test.go:50` | operator (buckets.sh:55) |
| `TestGameServer_PVCSurvivesPodDelete` | `test/e2e/gameserver_e2e_test.go:241` | operator (buckets.sh:59) |
| `TestGameServer_HeartbeatReachesRunning` | `test/e2e/gameserver_e2e_test.go:310` | operator (buckets.sh:58) |
| `TestGameServer_NetworkCaptureStartStopDownload` | `test/e2e/gameserver_e2e_test.go:370` | operator (buckets.sh:69) |
| `TestGameServer_NetworkCaptureEphemeralContainer` | `test/e2e/gameserver_e2e_test.go:854` | operator (buckets.sh:70) |
| `TestGameServer_NetworkCaptureRestartCleanup` | `test/e2e/gameserver_e2e_test.go:1092` | operator (buckets.sh:71) |
| `TestGameServer_NetworkCaptureConcurrencyRejected` | `test/e2e/gameserver_e2e_test.go:1281` | operator (buckets.sh:72) |
| `TestGameServer_InstallTimeDefaultApplies` | `test/e2e/gameserver_e2e_test.go:1467` | operator (buckets.sh:60) |
| `TestGameServer_TemplateDefaultOverridesInstallTime` | `test/e2e/gameserver_e2e_test.go:1532` | operator (buckets.sh:61) |
| `TestGameServer_NonexistentStorageClassSurfacesError` | `test/e2e/gameserver_e2e_test.go:1607` | operator (buckets.sh:63) |
| `TestGameServer_TunnelCredentialRefusalSurfaced` | `test/e2e/gameserver_e2e_test.go:1727` | operator (buckets.sh:67) |
| `TestGameServer_ExplicitStorageClassOverridesDefault` | `test/e2e/gameserver_e2e_test.go:1816` | operator (buckets.sh:62) |
| `TestGameServer_IdleNeverSleepsWithoutAPlayerCount` | `test/e2e/gameserver_idle_e2e_test.go:31` | operator (buckets.sh:57) |
| `TestCRD_Validation_IdleRejectsBadValues` | `test/e2e/gameserver_idle_e2e_test.go:120` | operator (buckets.sh:51) |
| `TestGameServer_SuspendScalesToZeroAndBack` | `test/e2e/gameserver_lifecycle_e2e_test.go:25` | operator (buckets.sh:56) |
| `TestGameServer_IngressNetworkPolicyShapeAndCascade` | `test/e2e/gameserver_networkpolicy_e2e_test.go:35` | operator (buckets.sh:68) |
| `TestGameServer_UnownedNamesakeSurvives` | `test/e2e/gameserver_ownership_e2e_test.go:30` | operator (buckets.sh:96) |
| `TestGameServer_VersionSwitch` | `test/e2e/gameserver_version_switch_e2e_test.go:23` | operator (buckets.sh:64) |
| `TestGameServer_WipeReportsFailureOnPermissionDenied` | `test/e2e/gameserver_wipe_e2e_test.go:23` | operator (buckets.sh:65) |
| `TestGameServer_GarrysModBot_Query` | `test/e2e/garrysmod_bot_e2e_test.go:29` | bot-fast (buckets.sh:237) |
| `TestMultiCluster_GatewayParity` | `test/e2e/gatewayparity_e2e_test.go:71` | multicluster (buckets.sh:339) |
| `TestHelmInstall_AllPodsReady` | `test/e2e/helm_install_e2e_test.go:27` | operator (buckets.sh:37) |
| `TestHelmInstall_AllCRDsPresent` | `test/e2e/helm_install_e2e_test.go:73` | operator (buckets.sh:38) |
| `TestHelmInstall_CRDApplyHookSkippedOnFreshInstall` | `test/e2e/helm_install_e2e_test.go:107` | operator (buckets.sh:39) |
| `TestHelmInstall_OperatorLogsClean` | `test/e2e/helm_install_e2e_test.go:128` | operator (buckets.sh:46) |
| `TestHelmInstall_APILogsClean` | `test/e2e/helm_install_e2e_test.go:160` | operator (buckets.sh:45) |
| `TestHelmInstall_APIHealthz` | `test/e2e/helm_install_e2e_test.go:195` | operator (buckets.sh:40) |
| `TestHelmInstall_MetricsNotOnPublicPort` | `test/e2e/helm_install_e2e_test.go:233` | operator (buckets.sh:41) |
| `TestHelmInstall_APIServerEgressPolicy_AllowsPrivateRanges` | `test/e2e/helm_install_e2e_test.go:276` | operator (buckets.sh:42) |
| `TestHelmInstall_BringYourOwnMTLSCA` | `test/e2e/helm_install_e2e_test.go:346` | operator (buckets.sh:43) |
| `TestHelmInstall_MTLSClientCertReissuedOnCAChange` | `test/e2e/helm_install_e2e_test.go:408` | operator (buckets.sh:44) |
| `TestGameServer_Left4Dead2Bot_Query` | `test/e2e/left4dead2_bot_e2e_test.go:20` | bot-heavy (buckets.sh:304) |
| `TestGameServer_MinecraftJavaBot_Joined` | `test/e2e/minecraft_bot_e2e_test.go:45` | bot-fast (buckets.sh:235) |
| `TestModule_DigestPinEnforcedAfterInstall` | `test/e2e/module_digest_pin_e2e_test.go:23` | operator (buckets.sh:79) |
| `TestModuleSourceAndModule` | `test/e2e/module_e2e_test.go:34` | operator (buckets.sh:81) |
| `TestModule_ScaffoldAndPackage` | `test/e2e/module_toolkit_e2e_test.go:27` | operator (buckets.sh:83) |
| `TestModule_SteamcmdScaffoldKeepsNonRootSecurity` | `test/e2e/module_toolkit_e2e_test.go:204` | operator (buckets.sh:84) |
| `TestModule_VerifyRejectsUnsignedBundle` | `test/e2e/module_verify_e2e_test.go:28` | operator (buckets.sh:77) |
| `TestModule_VerifySignedBundleInstalls` | `test/e2e/module_verify_signed_e2e_test.go:26` | operator (buckets.sh:78) |
| `TestModuleSource_RejectsSSRFTarget` | `test/e2e/modulesource_ssrf_e2e_test.go:27` | operator (buckets.sh:80) |
| `TestModuleSourceUpload` | `test/e2e/modulesource_upload_e2e_test.go:22` | operator (buckets.sh:82) |
| `TestGameServer_MountAndBlade2BannerlordBot_Query` | `test/e2e/mountandblade2bannerlord_bot_e2e_test.go:20` | bot-heavy (buckets.sh:305) |
| `TestMultiCluster_ClusterDispatchAndScopedRBAC` | `test/e2e/multicluster_e2e_test.go:203` | multicluster (buckets.sh:338) |
| `TestNetworkCapture_RetentionExpiry` | `test/e2e/networkcapture_retention_test.go:52` | operator (buckets.sh:73) |
| `TestGameServer_NuclearOptionBot_Query` | `test/e2e/nuclearoption_bot_e2e_test.go:22` | bot-heavy (buckets.sh:306) |
| `TestModule_FinalizerBlocksWhileTemplateInUse` | `test/e2e/operator_finalizer_e2e_test.go:26` | operator (buckets.sh:75) |
| `TestGameServer_CascadingDelete` | `test/e2e/operator_finalizer_e2e_test.go:178` | operator (buckets.sh:66) |
| `TestGameTemplate_DeletionWithLiveServer` | `test/e2e/operator_finalizer_e2e_test.go:252` | operator (buckets.sh:74) |
| `TestGameServer_PalworldBot_Query` | `test/e2e/palworld_bot_e2e_test.go:28` | bot-heavy (buckets.sh:307) |
| `TestAddressPool_DefaultPoolNoPreference` | `test/e2e/pool_assignment_e2e_test.go:30` | operator (buckets.sh:97) |
| `TestAddressPool_NamedPoolAssignment` | `test/e2e/pool_assignment_e2e_test.go:138` | operator (buckets.sh:98) |
| `TestAddressPool_NonLoadBalancerIgnored` | `test/e2e/pool_assignment_e2e_test.go:225` | operator (buckets.sh:99) |
| `TestAddressPool_NonexistentPoolError` | `test/e2e/pool_assignment_e2e_test.go:279` | operator (buckets.sh:100) |
| `TestAddressPool_ChangePoolOnRunningServer` | `test/e2e/pool_assignment_e2e_test.go:353` | operator (buckets.sh:101) |
| `TestAddressPool_RESTAPICarriesPool` | `test/e2e/pool_assignment_e2e_test.go:483` | operator (buckets.sh:102) |
| `TestAddressPool_StatusVisibleInAddressAssignmentCondition` | `test/e2e/pool_assignment_e2e_test.go:556` | operator (buckets.sh:103) |
| `TestAddressPool_ExplicitAddressRequest` | `test/e2e/pool_assignment_e2e_test.go:659` | operator (buckets.sh:104) |
| `TestAddressPool_AddressInUseConflict` | `test/e2e/pool_assignment_e2e_test.go:778` | operator (buckets.sh:105) |
| `TestAddressPool_PoolExhausted` | `test/e2e/pool_assignment_e2e_test.go:905` | operator (buckets.sh:106) |
| `TestGameServer_ProjectZomboidBot_Query` | `test/e2e/projectzomboid_bot_e2e_test.go:31` | bot-heavy (buckets.sh:308) |
| `TestRestore_RoundTrip` | `test/e2e/restore_e2e_test.go:29` | operator (buckets.sh:93) |
| `TestRestore_RejectsMissingBackup` | `test/e2e/restore_e2e_test.go:224` | operator (buckets.sh:94) |
| `TestGameServer_RustBot_Query` | `test/e2e/rust_bot_e2e_test.go:32` | bot-heavy (buckets.sh:309) |
| `TestGameServer_SatisfactoryBot_Query` | `test/e2e/satisfactory_bot_e2e_test.go:36` | bot-heavy (buckets.sh:310) |
| `TestGameServer_SevenDaysToDieBot_Query` | `test/e2e/sevendaystodie_bot_e2e_test.go:27` | bot-heavy (buckets.sh:311) |
| `TestGameServer_SquadBot_Query` | `test/e2e/squad_bot_e2e_test.go:20` | bot-heavy (buckets.sh:312) |
| `TestGameServer_Squad_RCON` | `test/e2e/squad_rcon_e2e_test.go:25` | bot-heavy (buckets.sh:313) |
| `TestGameServer_TeamFortress2Bot_Query` | `test/e2e/teamfortress2_bot_e2e_test.go:20` | bot-heavy (buckets.sh:314) |
| `TestGameServer_TeamFortress2_RCON` | `test/e2e/teamfortress2_rcon_e2e_test.go:26` | bot-heavy (buckets.sh:315) |
| `TestTelemetryLifecycle` | `test/e2e/telemetry_e2e_test.go:48` | telemetry (buckets.sh:358) |
| `TestGameServer_TerrariaBot_Joined` | `test/e2e/terraria_bot_e2e_test.go:38` | bot-fast (buckets.sh:236) |
| `TestGameServer_TheIsleBot_Query` | `test/e2e/theisle_bot_e2e_test.go:20` | bot-heavy (buckets.sh:316) |
| `TestGameServer_TModLoaderBot_Query` | `test/e2e/tmodloader_bot_e2e_test.go:20` | bot-heavy (buckets.sh:317) |
| `TestGameServer_TModLoader_Mods` | `test/e2e/tmodloader_mods_e2e_test.go:24` | bot-heavy (buckets.sh:318) |
| `TestUpgrade_FromPreviousRelease` | `test/e2e/upgrade_e2e_test.go:49` | upgrade (buckets.sh:349) |
| `TestGameServer_ValheimBot_Query` | `test/e2e/valheim_bot_e2e_test.go:33` | bot-heavy (buckets.sh:319) |
| `TestGameServer_VRisingBot_Query` | `test/e2e/vrising_bot_e2e_test.go:30` | bot-heavy (buckets.sh:320) |
| `TestGameServer_WakeOnConnect_PingDoesNotWake` | `test/e2e/wake_on_connect_e2e_test.go:28` | bot-fast (buckets.sh:238) |
| `TestGameServer_WakeOnConnect_LoginWakes` | `test/e2e/wake_on_connect_e2e_test.go:144` | bot-fast (buckets.sh:239) |
| `TestGameServer_WakeOnConnect_UnarmedNoSentinel` | `test/e2e/wake_on_connect_e2e_test.go:270` | bot-fast (buckets.sh:240) |

## Tests in no bucket

- None among the 156 test functions. `unbucketed()` (`buckets.sh:375`) is empty (`:; }`).
- `TestMain` (`test/e2e/e2e_suite_test.go:29`) is the harness, not a test. `suite_tests` filters it out with `grep -vx 'TestMain'` (`buckets.sh:413`).

## Bucket summaries

Bucket list order follows `bucket_names` (`buckets.sh`). Counts are from the bucket lists.

- **operator** (71, `buckets.sh:36`): Helm install and chart checks, CRD validation, GameServer reconciliation (children, suspend, idle, PVC, storage class, version switch, wipe, cascading delete, tunnel, ingress network policy, network capture), GameTemplate and Module finalizers, module verify and digest pin and SSRF, module scaffold, backups and backup schedules, restore, address pools, and one API event-stream and role-edit test. Zero admin logins.
- **api-auth** (10, login budget): bootstrap and login, login privacy, dynamic auth providers, audit events and pagination, logout and password-reset session invalidation, fake-OIDC Helm-seeded admin and default role mapping.
- **api-roles** (7): custom roles, built-in role immutability, per-namespace bindings, owner and collaborator access, auth role mappings, OIDC Helm override, user theme preferences.
- **api-rbac** (8): RBAC matrix for viewer, operator and admin, operator cannot invite users or reach captures, lifecycle clone and not-found.
- **api-agent** (7): agent proxy files, players and unreachable handling, console PTY round trip, logs WebSocket, start, stop and restart lifecycle.
- **api-mods** (7): mod manifest install and upgrade, mod upload, archive path-traversal and symlink confinement, owner-only server operations, bootstrap-admin force-ending sessions.
- **ratelimit** (1): API login rate limiter. Runs as its own last `go test` call so it does not starve other logins.
- **bot-fast** (6): joins and queries for minecraft-java, terraria and garrys-mod, plus wake-on-connect (ping does not wake, login wakes, unarmed has no sentinel).
- **bot-heavy** (32, `buckets.sh:288`): per-game bot query tests and RCON, mod and persistence tests for heavy games (storage, SteamCMD downloads). Per the comment at `buckets.sh:245`, this bucket is deliberately never run in CI and is only hand-run with `GAMEPLANE_E2E_GAMES=all`. Bucketed is not the same as executed.
- **multicluster** (4): account removal with SSO re-provisioning, module upload extraction budget, cluster dispatch and scoped RBAC across two clusters, gateway parity.
- **upgrade** (1): helm upgrade from the previous published release to the working tree.
- **telemetry** (1): telemetry lifecycle using `helm upgrade --reuse-values` on a dedicated cluster.
- **capture-overhead** (1, `buckets.sh:367`): capture-sidecar overhead gross-regression guard. Feeds a non-blocking CI job, not a blocking matrix leg.
