package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GameTemplateSpec defines a reusable blueprint for a game server.
// Users instantiate a GameTemplate by creating a GameServer that
// references it.
// +kubebuilder:validation:XValidation:rule="!has(self.consoleMode) || self.consoleMode != 'rcon' || (has(self.rcon) && (!has(self.rcon.protocol) || self.rcon.protocol != 'none'))",message="consoleMode 'rcon' requires spec.rcon with a protocol other than 'none'"
// +kubebuilder:validation:XValidation:rule="!has(self.capabilities) || !has(self.capabilities.mods) || !has(self.capabilities.mods.loaders) || size(self.capabilities.mods.loaders) == 0 || (has(self.versions) && size(self.versions) > 0)",message="capabilities.mods.loaders requires a non-empty spec.versions catalog (a mod loader is selected by versions[].loader)"
type GameTemplateSpec struct {
	// DisplayName is a human-friendly label shown in the dashboard.
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName"`

	// Game is the canonical game identifier (e.g. "minecraft-java",
	// "valheim", "factorio"). Used as a grouping key in the UI catalog.
	// +kubebuilder:validation:MinLength=1
	Game string `json:"game"`

	// Categories group this game in the dashboard's catalog and Create
	// Server picker (e.g. ["Survival", "Sandbox"]). A game may belong to
	// several at once — Minecraft is reasonably Sandbox, Survival and
	// Creative. The dashboard builds its category filter from the distinct
	// values present across installed templates, so a module introduces a
	// new category simply by naming it here — no frontend change. Empty
	// falls back to a heuristic on the game slug, and finally to "Other".
	// docs/module-authoring.md publishes the canonical vocabulary the
	// official modules use.
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:MaxLength=32
	// +kubebuilder:validation:items:MinLength=1
	// +optional
	Categories []string `json:"categories,omitempty"`

	// Version is the template revision (e.g. "1.0.0"). Bump when changing
	// defaults in ways that existing GameServers should opt into.
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// Icon is an optional URL or data URI shown in the catalog.
	// +optional
	Icon string `json:"icon,omitempty"`

	// AccentColor is an optional brand color (CSS hex, e.g. "#3b82f6")
	// the dashboard uses to tint this game's icon and accents. When
	// empty the dashboard falls back to a neutral default. This replaces
	// the previously hardcoded per-game color palette in the web app, so
	// new games carry their own color without a frontend change.
	// +kubebuilder:validation:Pattern=`^#[0-9a-fA-F]{6}$`
	// +optional
	AccentColor string `json:"accentColor,omitempty"`

	// Description is free-form markdown describing the template.
	// +optional
	Description string `json:"description,omitempty"`

	// Image is the default container image (e.g.
	// "itzg/minecraft-server:2025.1.0"). Used as the fallback when the
	// server selects no version (see Versions) and sets no spec.image
	// override. Can be overridden by GameServer.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Versions is an optional catalog of selectable game versions surfaced
	// in the Create Server wizard. Each entry maps a user choice to a
	// concrete image (and optional per-version env / mod loader). When empty
	// there is no version choice: a server runs spec.image (override) or this
	// template's Image, exactly as before. At most one entry should set
	// default=true; otherwise the first entry is the wizard's default.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Versions []GameVersion `json:"versions,omitempty"`

	// Command / Args override the container image entrypoint when set.
	// +optional
	Command []string `json:"command,omitempty"`
	// +optional
	Args []string `json:"args,omitempty"`

	// Env is the default environment for the game container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Ports declares exposed ports on the game container.
	// +optional
	Ports []GamePort `json:"ports,omitempty"`

	// Storage describes the default persistent storage layout.
	// +optional
	Storage GameStorageSpec `json:"storage,omitempty"`

	// Resources are the default compute resources for the game container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// RCON declares the remote-console protocol used by this game, if any.
	// +optional
	RCON *RCONSpec `json:"rcon,omitempty"`

	// ConsoleMode controls how the dashboard's Console tab attaches to
	// the running game.
	//
	//   - "rcon": send line-based RCON commands (default when RCON is set
	//     and its protocol is not "none").
	//   - "pty":  attach to the game container's stdin/stdout via the
	//     Kubernetes pod-attach API. Requires the container to be started
	//     with tty=true and stdin=true (the operator sets these). Switching
	//     to pty after the pod exists requires a pod recreate.
	//   - "none": disable the Console tab for this game.
	//
	// When unset, the operator defaults to "rcon" if RCON.Protocol is set
	// to anything other than "none", and "none" otherwise.
	// +kubebuilder:validation:Enum=rcon;pty;none
	// +optional
	ConsoleMode string `json:"consoleMode,omitempty"`

	// LogPath is the file holding the game's primary log, as seen from
	// inside the pod (e.g. "/data/logs/latest.log"). It must live under
	// the shared data volume (Storage.MountPath) so the agent sidecar
	// can tail it for the dashboard's Logs tab. Leave empty for games
	// that only log to stdout — the Logs tab is then unavailable.
	// +optional
	LogPath string `json:"logPath,omitempty"`

	// Probes are the default readiness/liveness/startup probes for the
	// game container. Operator supplies sane defaults when unset.
	// +optional
	Probes *GameProbesSpec `json:"probes,omitempty"`

	// ConfigSchema declares user-tunable fields surfaced in the Create
	// Server wizard. The operator resolves GameServer.spec.config
	// against this schema (applying defaults, validating types/enums)
	// and sets each resolved value as an env var on the game container.
	// Fields with target=file are consumed by ConfigFiles templates
	// instead of becoming env vars.
	// +optional
	ConfigSchema []ConfigField `json:"configSchema,omitempty"`

	// ConfigFiles declares files the operator renders from the resolved
	// config values and places under Storage.MountPath before the game
	// starts. Rendered contents are stored in an owned `<server>-files`
	// Secret (they may embed password values) and copied onto the data
	// volume by an init container on every pod start — manual edits to
	// these paths via the Files tab are overwritten on restart.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	ConfigFiles []ConfigFile `json:"configFiles,omitempty"`

	// Agent tunes the sidecar deployed alongside the game container.
	// +optional
	Agent *AgentSpec `json:"agent,omitempty"`

	// Capabilities declares the game-specific console commands behind
	// agent features (player moderation, backup quiesce). The operator
	// serializes this onto the agent sidecar, which interprets it at
	// runtime — modules add full feature support without agent code
	// changes. All commands run over the template's RCON connection,
	// so they require rcon.protocol != none.
	// +optional
	Capabilities *CapabilitiesSpec `json:"capabilities,omitempty"`

	// Security overrides the pod/container security settings for games whose
	// image refuses to run as root or expects a specific uid (e.g. ARK's
	// image requires uid 25000 and cannot initialise Proton as root).
	// Omitted means today's behaviour: the image's own default user.
	// +optional
	Security *GameSecuritySpec `json:"security,omitempty"`
}

// GameSecuritySpec overrides the uid/gid the game container runs as and
// the gid the kubelet chowns the mounted data volume to. Unlike the
// agent sidecar (always distroless, always a fixed non-root uid), the
// game container runs whatever image the module ships, so its user is
// not something the operator can infer — a template opts in explicitly.
type GameSecuritySpec struct {
	// RunAsUser is the uid the GAME container runs as.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4294967295
	// +optional
	RunAsUser *int64 `json:"runAsUser,omitempty"`

	// RunAsGroup is the gid the GAME container runs as.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4294967295
	// +optional
	RunAsGroup *int64 `json:"runAsGroup,omitempty"`

	// FSGroup makes the kubelet chown the mounted volumes to this gid on
	// start, so a non-root game user can write to its PVC. This is what
	// replaces the chown init-container such images normally ship with.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4294967295
	// +optional
	FSGroup *int64 `json:"fsGroup,omitempty"`
}

// GameVersion is one selectable entry in a template's version catalog.
// Selecting it (via GameServer.spec.version) pins the container image,
// appends this entry's env, and — when Loader names a capabilities.mods
// loader — provisions and mounts that loader's per-(version+loader) mod
// volume.
type GameVersion struct {
	// ID is the stable selector stored in GameServer.spec.version. It is
	// also folded into the per-version mod volume/PVC names, so keep it short
	// and DNS-ish (dots and hyphens allowed; sanitized to a DNS label for
	// volume names). E.g. "1.21.4-paper", "tmodloader-latest".
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	ID string `json:"id"`

	// DisplayName labels the entry in the version picker, e.g.
	// "1.21.4 (Paper)".
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DisplayName string `json:"displayName"`

	// Image is the full container reference for this version. For
	// env-versioned games (e.g. itzg/minecraft-server, which picks software
	// via TYPE/VERSION env) this is usually one pinned image shared across
	// entries, differentiated only by Loader/Env. For tag-versioned games
	// (e.g. Terraria) each entry pins a distinct tag.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Loader is the mod-loader / server-type key (e.g. "paper", "forge",
	// "fabric", "vanilla", "bepinex", "tmodloader"). It keys into
	// capabilities.mods.loaders to select this combo's mod volume. Empty
	// means this version has no loader dimension (mods, if any, live on the
	// shared data volume).
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	// +optional
	Loader string `json:"loader,omitempty"`

	// Env is appended when this version is selected (e.g. TYPE=PAPER,
	// VERSION=1.21.4 for itzg). It is applied after the template's Env and
	// before GameServer.spec.env, so an explicit user override still wins.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Default marks the wizard's pre-selected entry. At most one entry should
	// set it; if none (or several) do, the first entry is the default.
	// +optional
	Default bool `json:"default,omitempty"`

	// GameVersion is the upstream game-version token the dashboard passes to
	// an external mod registry to filter results to this version (e.g.
	// Modrinth's "game_versions" facet), e.g. "1.21.4". It is distinct from
	// ID (a Gameplane selector like "1.21.4-paper" that's too lossy to parse
	// reliably). Optional; when unset the registry search sends no version
	// facet and returns mods for all versions. Ignored by registries with no
	// version dimension (e.g. Thunderstore) and when the template declares no
	// mods.registry.
	// +kubebuilder:validation:MaxLength=32
	// +optional
	GameVersion string `json:"gameVersion,omitempty"`
}

// CapabilitiesSpec declares the per-game command surface the agent
// interprets.
type CapabilitiesSpec struct {
	// Players configures the moderation actions on the Players tab.
	// +optional
	Players *PlayerActionsSpec `json:"players,omitempty"`

	// Quiesce configures how in-flight game state is flushed to disk
	// before a backup snapshot (and resumed afterwards).
	// +optional
	Quiesce *QuiesceSpec `json:"quiesce,omitempty"`

	// Lifecycle configures graceful lifecycle transitions the agent drives
	// over RCON — currently the in-game stop sequence the operator runs
	// before scaling the server down, so the world is saved and shut down
	// cleanly instead of relying on a container SIGTERM. Requires
	// rcon.protocol != none.
	// +optional
	Lifecycle *LifecycleSpec `json:"lifecycle,omitempty"`

	// Actions declares named operator actions surfaced as buttons on the
	// server detail page. Each runs a templated console command over the
	// template's RCON connection, so they require rcon.protocol != none.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	Actions []ServerActionSpec `json:"actions,omitempty"`

	// Status declares game-specific live metrics shown on the Overview
	// tab, each read via an RCON command parsed by a named-group regex.
	// Like actions, they require rcon.protocol != none.
	// +optional
	Status *StatusSpec `json:"status,omitempty"`

	// Mods declares how the dashboard manages this game's mods/plugins.
	// The dashboard is generic — it lists, installs, and removes mods by
	// calling the agent; this block tells the agent where mods live and
	// how installs are permitted. Mods are files under a directory on the
	// game's data volume, so this needs no RCON.
	// +optional
	Mods *ModsSpec `json:"mods,omitempty"`
}

// ModsSpec declares the mod/plugin directory and install policy the
// agent enforces. A template uses EITHER the single shared Path (legacy,
// one mods dir on the data volume) OR Loaders (a per-(version+loader) mod
// volume selected by the active GameVersion.loader) OR IDList (the server
// downloads its own mods given a list of ids, so there is no mods
// directory at all — see ModIDListSpec). Listing and removal operate on
// the resolved directory; Install adds a mod by downloading it there.
// IDList games skip Install/Registry entirely: the dashboard just resolves
// GameServer.spec.mods.ids some other way (e.g. a registry search) and the
// operator projects them, no agent download involved.
// +kubebuilder:validation:XValidation:rule="has(self.path) || (has(self.loaders) && size(self.loaders) > 0) || has(self.idList)",message="mods requires path, at least one loaders entry, or idList"
type ModsSpec struct {
	// Path is the single shared mods directory, relative to
	// storage.mountPath (e.g. "mods" for Forge/Fabric, "plugins" for
	// Bukkit/Paper). Used when Loaders is empty. Absolute paths and ".."
	// segments are rejected.
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('/') && !self.contains('..')",message="path must be relative to the data mount and must not contain '..'"
	// +optional
	Path string `json:"path,omitempty"`

	// Loaders maps a loader id (from GameVersion.loader) to its mods
	// directory. When the active version's loader has an entry here, the
	// operator provisions a per-(version+loader) PVC, mounts it at
	// storage.mountPath/<path> on the game and agent containers, and points
	// the agent's mod manager at it — so each version+loader keeps its own
	// mod set. When empty, Path is the single shared mods dir (legacy).
	// MaxProperties bounds the per-entry CEL validation cost (the apiserver
	// rejects the CRD otherwise).
	// +kubebuilder:validation:MaxProperties=16
	// +optional
	Loaders map[string]ModLoaderSpec `json:"loaders,omitempty"`

	// Extensions optionally restricts which files in Path are treated as
	// mods (e.g. [".jar"]). Empty lists every file. Used with Path; per-
	// loader extensions are set on each ModLoaderSpec instead.
	// +optional
	Extensions []string `json:"extensions,omitempty"`

	// Extract is the resolved per-loader Extract flag (the operator copies
	// it from the active loader). When true the agent unpacks archive mods
	// into per-mod folders. Not normally set by template authors at this
	// level — set it on the loader entry instead.
	// +optional
	Extract bool `json:"extract,omitempty"`

	// Install, when set, lets the dashboard add new mods by downloading
	// them into the resolved mods directory. When unset, only listing and
	// removal are offered. Shared across all loaders.
	// +optional
	Install *ModInstallSpec `json:"install,omitempty"`

	// Registry, when set, lets the dashboard browse and search an external
	// mod registry for this game (in addition to install-by-URL). Gameplane
	// ships the provider engines and a generic browse UI; the module selects
	// a provider here and the agent's Install downloads the chosen file —
	// so the registry's CDN must also be in Install.AllowedHosts. Omit for
	// URL-only games (e.g. tModLoader, whose mods live on Steam Workshop).
	// +optional
	Registry *ModRegistrySpec `json:"registry,omitempty"`

	// IDList declares that this game installs mods by ID rather than by file:
	// the server downloads its own mods given a list of ids, so the operator
	// projects the GameServer's selected ids into a game-container env var
	// instead of the agent downloading files into a mods directory.
	// Mutually exclusive with Path/Loaders in practice — a game does one or
	// the other.
	// +optional
	IDList *ModIDListSpec `json:"idList,omitempty"`
}

// ModIDListSpec declares that this game's server downloads its own mods
// given a list of provider-native ids (e.g. ARK: Survival Ascended's
// CurseForge ids appended to its launch string, Project Zomboid's
// semicolon-separated MOD_IDS env, Steam Workshop id lists) rather than
// the agent dropping files into a mods directory. The operator renders
// GameServer.spec.mods.ids into Env; there is no agent involvement.
type ModIDListSpec struct {
	// Env is the game-container environment variable the rendered id list
	// is projected into (e.g. "ASA_START_PARAMS", "MOD_IDS").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Env string `json:"env"`

	// Separator joins the ids. Defaults to ",".
	// +kubebuilder:default=","
	// +kubebuilder:validation:MaxLength=4
	// +optional
	Separator string `json:"separator,omitempty"`

	// Format renders the joined id list into the env value. The literal
	// token "{{ids}}" is replaced with the separator-joined ids. This is a
	// plain string replacement, NOT a Go text/template — a module bundle
	// must not be able to execute template logic through this field.
	// Defaults to "{{ids}}". ARK uses " -mods={{ids}}" so the ids append
	// onto its existing launch parameters.
	// +kubebuilder:default="{{ids}}"
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Format string `json:"format,omitempty"`

	// Mode is how the rendered value is applied to Env:
	// "replace" (default) sets Env to the rendered value;
	// "append" concatenates the rendered value onto Env's existing value
	// (used when the same env also carries user-supplied launch params).
	// +kubebuilder:validation:Enum=replace;append
	// +kubebuilder:default=replace
	// +optional
	Mode string `json:"mode,omitempty"`
}

// ModRegistrySpec lists the built-in external mod registries the dashboard
// can browse for this game. The engines are generic Gameplane code; this
// block is the per-game configuration that drives them. Loader filtering
// reuses the active version's loader id verbatim and version filtering uses
// the active GameVersion.gameVersion token — so no mappings live here.
type ModRegistrySpec struct {
	// Providers is the ordered list of registries to offer. The dashboard
	// shows a provider switch when there's more than one; the first is the
	// default. A provider whose engine needs unmet config (e.g. a
	// CurseForge API key) is hidden until configured.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	Providers []ModProvider `json:"providers"`
}

// ModProvider configures one registry engine for a game.
//
// +kubebuilder:validation:XValidation:rule="self.provider != 'steam' || (has(self.steamAppID) && self.steamAppID > 0)",message="steamAppID is required when provider is steam"
// +kubebuilder:validation:XValidation:rule="self.provider != 'github' || has(self.github)",message="github is required when provider is github"
// +kubebuilder:validation:XValidation:rule="self.provider != 'curseforge' || (has(self.curseforgeGameID) && self.curseforgeGameID > 0)",message="curseforgeGameID is required when provider is curseforge"
type ModProvider struct {
	// Provider names the built-in registry engine: "modrinth" (Minecraft
	// mods/plugins, keyless), "thunderstore" (BepInEx games, keyless,
	// per-community), "curseforge" (mods/modpacks for the game identified
	// by CurseForgeGameID, needs an API key), "hangar" (PaperMC plugins,
	// keyless), "factorio" (the official Factorio mod portal; browse is
	// keyless, downloads need the player's own factorio.com credentials so
	// installs hand off to the from-URL form), "steam" (Steam Workshop
	// browse, needs a Steam Web API key; see SteamAppID — Workshop content
	// has no download URL, so it's a preview-only browser wired to
	// modpacks.refEnv for collection-based games like Garry's Mod/CS2),
	// "nexus" (Nexus Mods, needs an API key, browse-only for the same
	// reason as steam — see Community for its per-game domain slug),
	// "spigot" (SpigotMC plugins via the Spiget API, keyless), "github"
	// (one repository's Releases stand in for versions, keyless but
	// rate-limited — see GitHub), or "umod" (Rust/Hurtworld/7 Days to
	// Die's Oxide/uMod plugin ecosystem, keyless).
	// +kubebuilder:validation:Enum=modrinth;thunderstore;curseforge;hangar;factorio;steam;spigot;github;umod;nexus
	Provider string `json:"provider"`

	// Community is the Thunderstore community slug whose package index to
	// search, e.g. "valheim" (required by the thunderstore provider), or
	// the Nexus Mods game domain slug, e.g. "skyrimspecialedition"
	// (required by the nexus provider). Ignored by others.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	Community string `json:"community,omitempty"`

	// SteamAppID is the Steam application id this provider browses (e.g.
	// 4000 for Garry's Mod, 730 for Counter-Strike 2) — it facets the
	// Workshop search to that app's items/collections. Required when
	// Provider is "steam"; ignored otherwise.
	// +kubebuilder:validation:Minimum=1
	// +optional
	SteamAppID int32 `json:"steamAppID,omitempty"`

	// CurseForgeGameID is the numeric CurseForge game the browser searches
	// (Minecraft is 432; ARK: Survival Ascended is 83374 — from CurseForge's
	// own /v1/games). Required when provider is curseforge: the API has no
	// safe default, and guessing one is how ARK's browser ended up listing
	// Minecraft mods.
	// +optional
	CurseForgeGameID *int32 `json:"curseforgeGameID,omitempty"`

	// GitHub binds this provider to one repository's Releases. GitHub has
	// no cross-repo mod search (unlike Thunderstore's per-community
	// index), so a template picks exactly one repo to browse. Required
	// when Provider is "github"; ignored otherwise.
	// +optional
	GitHub *GitHubRepoSpec `json:"github,omitempty"`

	// CredentialsSecretRef references a Secret in the GameServer's namespace
	// that holds registry download credentials. The Secret must contain
	// "username" and "token" keys. Currently used by the "factorio" provider
	// for mod-portal downloads. The agent injects these credentials
	// transparently during installs.
	// +optional
	CredentialsSecretRef *SecretNameRef `json:"credentialsSecretRef,omitempty"`

	// Modpacks, when set, surfaces a Modpacks browser for this provider and
	// declares how installing one is applied. A modpack is selected as a
	// whole (not added to the mods dir like a single mod), so install
	// either pins it via a game-image env (RefEnv) or resolves and installs
	// its dependency mods. Omit for providers without modpacks. Steam
	// Workshop collections are always env-mode: set RefEnv to the game
	// image's collection-id variable (e.g. "WORKSHOP_COLLECTION") since
	// steamcmd resolves a collection's members itself inside the container.
	// +optional
	Modpacks *ModpackSpec `json:"modpacks,omitempty"`
}

// GitHubRepoSpec identifies the single GitHub repository the "github" mod
// registry provider browses (its Releases stand in for versions). Both
// fields are bounded to GitHub's own login/repo-name limits.
type GitHubRepoSpec struct {
	// Owner is the repository owner (user or organization login).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=39
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9-]*$`
	Owner string `json:"owner"`

	// Repo is the repository name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._-]+$`
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="repo must not contain '..'"
	Repo string `json:"repo"`
}

// ModpackSpec declares how a chosen modpack is installed for a game.
type ModpackSpec struct {
	// RefEnv, when set, is the game-image env the operator points at the
	// chosen modpack reference (slug/URL) — e.g. "MODRINTH_MODPACK" for the
	// itzg image. Installing then patches GameServer.spec.env and restarts;
	// one modpack is active per server. When empty, installing instead
	// resolves and installs the modpack's dependency mods (e.g. a
	// Thunderstore/BepInEx pack).
	// +kubebuilder:validation:MaxLength=64
	// +optional
	RefEnv string `json:"refEnv,omitempty"`

	// Env are additional fixed env applied alongside RefEnv when a modpack
	// is active, e.g. {TYPE: MODRINTH} to switch the itzg image into
	// Modrinth-modpack mode. Bounded to keep the CRD small.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// ModLoaderSpec is the mods directory for one loader / server-type,
// selected by the active GameVersion.loader. The operator provisions a
// per-(version+loader) PVC and mounts it at storage.mountPath/<Path> on
// both the game container (where the image reads mods) and the agent.
type ModLoaderSpec struct {
	// Path is this loader's mods/plugins directory, relative to
	// storage.mountPath (e.g. "plugins", "mods", "bepinex/plugins",
	// "ModPacks"). Absolute paths and ".." segments are rejected. MaxLength
	// is kept small to bound the per-entry CEL validation cost (this rule
	// lives inside the loaders map, so its cost is multiplied per entry).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('/') && !self.contains('..')",message="path must be relative to the data mount and must not contain '..'"
	Path string `json:"path"`

	// DisplayName labels this volume in the Mods tab, e.g. "Plugins (Paper)".
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Extensions optionally restricts which files are treated as mods for
	// this loader (e.g. [".jar"], [".dll"]). Empty lists every file.
	// +optional
	Extensions []string `json:"extensions,omitempty"`

	// Extract, when true, tells the agent to treat downloaded mods as
	// archives (e.g. Thunderstore .zip): each install unpacks into its own
	// folder under the mods dir so the loader (e.g. BepInEx, which scans
	// recursively) finds the contained files. Listing/removal then operate
	// on those per-mod folders. Use for loaders distributed as archives.
	// +optional
	Extract bool `json:"extract,omitempty"`
}

// ModInstallSpec configures installing a mod by downloading it from a
// URL into the mods directory.
type ModInstallSpec struct {
	// AllowedHosts is the allowlist of hosts the agent may download from:
	// an exact hostname ("cdn.modrinth.com") or a leading-dot suffix
	// (".modrinth.com") matching that host and its subdomains. Downloads
	// from any other host — and any that resolve to a private, loopback,
	// or link-local address — are refused (SSRF guard).
	// +kubebuilder:validation:MinItems=1
	AllowedHosts []string `json:"allowedHosts"`

	// MaxSizeMB caps the download size in mebibytes. Defaults to 256.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=4096
	// +optional
	MaxSizeMB int32 `json:"maxSizeMB,omitempty"`
}

// ServerActionSpec declares a named operator action surfaced as a button
// on the server detail page. Running it renders Command (a Go
// text/template) with the collected parameters and sends the result over
// the template's RCON connection.
// +kubebuilder:validation:XValidation:rule="has(self.command) != (has(self.commands) && size(self.commands) > 0)",message="set exactly one of command or commands"
type ServerActionSpec struct {
	// ID is a stable identifier, unique within the template's actions.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	ID string `json:"id"`

	// DisplayName is the button label.
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName"`

	// Description explains the action; shown in the confirmation dialog.
	// +optional
	Description string `json:"description,omitempty"`

	// Icon is an optional lucide-react icon name (e.g. "megaphone").
	// +optional
	Icon string `json:"icon,omitempty"`

	// Command is a Go text/template rendered with .Params (each declared
	// parameter name mapped to its resolved value) and sent over RCON,
	// e.g. "say {{.Params.message}}". Parameter values are sanitized for
	// console-injection before rendering.
	// +kubebuilder:validation:MinLength=1
	// +optional
	Command string `json:"command,omitempty"`

	// Commands runs several console commands in order (mutually exclusive
	// with Command). Mirrors capabilities.quiesce / lifecycle.stop, which
	// already express sequences as a plain []string. Each is a Go template
	// rendered with .Params, same as Command.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Commands []string `json:"commands,omitempty"`

	// Transport selects how the commands reach the game. Empty resolves to
	// rcon when rcon.protocol != none, else stdin (pod-attach) — so a
	// pty-console game like Terraria can carry actions too.
	// +kubebuilder:validation:Enum=rcon;stdin
	// +optional
	Transport string `json:"transport,omitempty"`

	// Group labels a section on the server detail page (e.g. "World").
	// +kubebuilder:validation:MaxLength=24
	// +optional
	Group string `json:"group,omitempty"`

	// Params declares inputs collected from the user before running.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Params []ActionParamSpec `json:"params,omitempty"`

	// Confirm, when true, makes the dashboard require explicit
	// confirmation before running the action.
	// +optional
	Confirm bool `json:"confirm,omitempty"`

	// Danger marks a destructive action so the dashboard styles it
	// distinctly (e.g. a red button).
	// +optional
	Danger bool `json:"danger,omitempty"`
}

// ActionParamSpec is a single input collected before running an action.
type ActionParamSpec struct {
	// Name is the parameter identifier referenced in the command
	// template as {{.Params.<name>}}.
	// +kubebuilder:validation:Pattern=`^[a-zA-Z_][a-zA-Z0-9_]*$`
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// DisplayName labels the input in the UI.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Description explains the input.
	// +optional
	Description string `json:"description,omitempty"`

	// Type controls the input widget and validation.
	// +kubebuilder:validation:Enum=string;int;bool;enum
	// +kubebuilder:default=string
	Type string `json:"type"`

	// Default is the pre-filled value (as a string).
	// +optional
	Default string `json:"default,omitempty"`

	// CurrentFrom names a status metric (spec.capabilities.status.metrics[].id)
	// whose live value pre-fills this input in the dashboard dialog, so a
	// "set" action opens on the current setting. Default applies when the
	// metric has no reading or (for Type=enum) no option matches it.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +optional
	CurrentFrom string `json:"currentFrom,omitempty"`

	// Enum restricts valid values when Type=enum.
	// +optional
	Enum []string `json:"enum,omitempty"`

	// Required, when true, blocks running until the input is set.
	// +optional
	Required bool `json:"required,omitempty"`
}

// StatusSpec declares game-specific live metrics for the Overview tab.
type StatusSpec struct {
	// Metrics are the per-game readouts. Each runs an RCON command and
	// extracts a value via a named-group regex.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Metrics []StatusMetricSpec `json:"metrics,omitempty"`
}

// StatusMetricSpec reads one live metric from an RCON command's output.
type StatusMetricSpec struct {
	// ID is a stable identifier, unique within the template's metrics.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	ID string `json:"id"`

	// DisplayName labels the metric in the UI.
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName"`

	// Command is the RCON command whose output is parsed.
	// +kubebuilder:validation:MinLength=1
	Command string `json:"command"`

	// Regex extracts the value via the named group "value", e.g.
	// `TPS: (?P<value>[0-9.]+)`.
	// +kubebuilder:validation:MinLength=1
	Regex string `json:"regex"`

	// Unit is an optional suffix shown after the value (e.g. "ms", "TPS").
	// +optional
	Unit string `json:"unit,omitempty"`
}

// PlayerActionsSpec maps moderation actions to console commands. Each
// command is a Go text/template rendered with .Player and .Reason
// (reason may be empty — guard with {{if .Reason}}). Unset actions are
// reported as unsupported.
type PlayerActionsSpec struct {
	// Kick disconnects a player, e.g.
	// "kick {{.Player}}{{if .Reason}} {{.Reason}}{{end}}".
	// +optional
	Kick string `json:"kick,omitempty"`

	// Ban bans a player.
	// +optional
	Ban string `json:"ban,omitempty"`

	// Unban lifts a ban, e.g. "pardon {{.Player}}".
	// +optional
	Unban string `json:"unban,omitempty"`

	// BanList configures reading the current ban list.
	// +optional
	BanList *BanListSpec `json:"banList,omitempty"`

	// Whitelist configures the allow-list management actions on the
	// Players tab (list / add / remove).
	// +optional
	Whitelist *WhitelistSpec `json:"whitelist,omitempty"`

	// List configures how the online player list is read.
	// +optional
	List *PlayerListSpec `json:"list,omitempty"`
}

// WhitelistSpec declares how to manage the game's whitelist (allow list).
// Add/Remove are Go text/templates rendered with .Player; List is a plain
// command whose output is parsed by ListRegex.
type WhitelistSpec struct {
	// List prints the current whitelist (e.g. "whitelist list").
	// +kubebuilder:validation:MinLength=1
	List string `json:"list"`

	// Add adds a player, e.g. "whitelist add {{.Player}}".
	// +kubebuilder:validation:MinLength=1
	Add string `json:"add"`

	// Remove removes a player, e.g. "whitelist remove {{.Player}}".
	// +kubebuilder:validation:MinLength=1
	Remove string `json:"remove"`

	// ListRegex extracts whitelisted names from the List output. If it has
	// a named group "names", that group is split on commas (Minecraft's
	// "There are N whitelisted players: a, b, c"); otherwise each line's
	// "name" group is one entry.
	// +kubebuilder:validation:MinLength=1
	ListRegex string `json:"listRegex"`
}

// BanListSpec reads and parses the game's ban list.
type BanListSpec struct {
	// Command prints the ban list (e.g. "banlist players").
	// +kubebuilder:validation:MinLength=1
	Command string `json:"command"`

	// EntryRegex matches one banned player per output line, using the
	// named groups "name" (required), "source" and "reason" (optional).
	// +kubebuilder:validation:MinLength=1
	EntryRegex string `json:"entryRegex"`
}

// PlayerListSpec configures the command that prints the online player
// list and how to parse its output.
type PlayerListSpec struct {
	// Command is the console/RCON command that prints the online players (e.g. "list").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Command string `json:"command"`

	// EntryRegex optionally extracts one player name per match from the
	// command output (first capture group, or whole match if no group).
	// ^ and $ match per line (multiline mode).
	// When empty the built-in parser is used.
	// +optional
	// +kubebuilder:validation:MaxLength=512
	EntryRegex string `json:"entryRegex,omitempty"`
}

// QuiesceSpec declares the command sequences that pause and resume
// game writes around a backup.
type QuiesceSpec struct {
	// Quiesce runs before the snapshot, in order (e.g. ["save-off",
	// "save-all flush"]). Any command error triggers a best-effort
	// Unquiesce.
	// +kubebuilder:validation:MinItems=1
	Quiesce []string `json:"quiesce"`

	// Unquiesce runs after the snapshot (e.g. ["save-on"]).
	// +kubebuilder:validation:MinItems=1
	Unquiesce []string `json:"unquiesce"`

	// FailurePattern, when it matches a quiesce command's output
	// (case-insensitive regex), treats the step as failed even though
	// the command itself returned successfully.
	// +optional
	FailurePattern string `json:"failurePattern,omitempty"`
}

// LifecycleSpec declares command sequences the agent runs over RCON to
// drive graceful lifecycle transitions.
type LifecycleSpec struct {
	// Stop runs, in order, to shut the game down cleanly before the
	// operator scales the server to zero (e.g. ["stop"] for Minecraft, or
	// a warn-then-shutdown sequence). The operator issues these, waits for
	// the game to go not-ready (or for GameServer.spec.stopGracePeriodSeconds
	// to elapse), then removes the pod — so a SIGTERM never interrupts an
	// in-progress save.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Stop []string `json:"stop"`
}

// GamePort is a single exposed port.
type GamePort struct {
	// Name is a DNS-label port identifier (e.g. "game", "query", "rcon").
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// ContainerPort is the port the game listens on inside the pod.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ContainerPort int32 `json:"containerPort"`

	// Protocol is one of TCP, UDP. Defaults to TCP.
	// +kubebuilder:default=TCP
	// +kubebuilder:validation:Enum=TCP;UDP
	// +optional
	Protocol corev1.Protocol `json:"protocol,omitempty"`

	// Advertise controls whether this port is exposed to users via
	// GameServer.Spec.Networking. RCON and query ports are typically
	// not advertised publicly.
	//
	// No `omitempty`: the CRD default is true, so an `omitempty` tag would
	// let the typed client silently drop an explicit `false` on the wire
	// (Go's zero value for bool), and the apiserver would re-apply the
	// `true` default — putting RCON/query ports back on the Service and
	// the game-ingress NetworkPolicy. Do not add it back.
	// +kubebuilder:default=true
	// +optional
	Advertise bool `json:"advertise"`

	// WakeProtocol selects the parser the wake sentinel applies to this port
	// while the server is asleep. "minecraft" and "terraria" parse the real
	// handshake, so only a genuine join wakes the server and a server-list
	// ping is answered in place without waking it. "generic" wakes on
	// plausible traffic — the only option for the UDP-only games, which have
	// no connection to hold. "none" never wakes.
	// +kubebuilder:validation:Enum=minecraft;terraria;generic;none
	// +kubebuilder:default=generic
	// +optional
	WakeProtocol string `json:"wakeProtocol,omitempty"`
}

// GameStorageSpec describes the persistent storage layout for a game.
type GameStorageSpec struct {
	// Size is the default PVC size (e.g. "10Gi").
	// +optional
	Size resource.Quantity `json:"size,omitempty"`

	// StorageClassName, when set, pins the PVC to a specific StorageClass.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// MountPath is where the game persistent volume is mounted inside
	// the game container (e.g. "/data").
	// +kubebuilder:default=/data
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// DataSource, when set on a GameServer, seeds the data PVC from an
	// existing CSI VolumeSnapshot the first time the PVC is created. This
	// is how volume-snapshot Restores stand up a new server from a
	// snapshot. It is immutable once the PVC binds and has no effect when
	// set on a GameTemplate (it is a per-server restore concern).
	// +optional
	DataSource *GameDataSource `json:"dataSource,omitempty"`

	// Extra declares additional persistent volumes beyond the primary data
	// volume. Use it when a game's persistent state lives in several
	// directories with no safe common parent — e.g. 7 Days to Die keeps its
	// 17GB install in serverfiles/ and its worlds in .local/share/, whose only
	// shared parent also holds the entrypoint script, so mounting there would
	// shadow it. This same field lives on GameServer.spec.storage (an alias
	// of GameStorageSpec): a GameServer that sets a non-empty Extra replaces
	// the template's list wholesale, mirroring how Size/StorageClassName
	// already override the template on that field — it does not merge
	// entry-by-entry.
	// +kubebuilder:validation:MaxItems=4
	// +optional
	Extra []ExtraVolumeSpec `json:"extra,omitempty"`
}

// ExtraVolumeSpec declares one additional persistent volume beyond a
// GameTemplate's primary data volume (see GameStorageSpec.Extra). The
// operator provisions one PVC per entry using the same mechanism as the
// primary data PVC (create-if-absent, ReadWriteOnce, owned by the
// GameServer) under its own "<server>-extra-<name>" PVC name, and mounts it
// directly at MountPath on the game container — never nested under the
// primary Storage.MountPath, since these exist precisely for directories
// that share no safe common parent with it.
type ExtraVolumeSpec struct {
	// Name identifies the volume and forms part of its PVC name. Must be a
	// DNS-1123 label.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	Name string `json:"name"`

	// MountPath is the absolute in-container path this volume is mounted at.
	// +kubebuilder:validation:Pattern=`^/.*`
	// +kubebuilder:validation:MaxLength=255
	MountPath string `json:"mountPath"`

	// Size is the requested capacity (e.g. "40Gi").
	Size resource.Quantity `json:"size"`
}

// GameDataSource references a CSI VolumeSnapshot used to pre-populate a
// freshly-created data PVC. Only Kind=VolumeSnapshot
// (snapshot.storage.k8s.io) is supported.
type GameDataSource struct {
	// Kind is the data-source object kind. Only "VolumeSnapshot" is
	// supported today.
	// +kubebuilder:validation:Enum=VolumeSnapshot
	// +kubebuilder:default=VolumeSnapshot
	Kind string `json:"kind"`

	// Name is the VolumeSnapshot object name in the GameServer's namespace.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// RCONSpec describes the remote-console protocol used by the game.
// +kubebuilder:validation:XValidation:rule="!has(self.authentication) || self.authentication || (!has(self.passwordSecretRef) && (!has(self.passwordEnv) || size(self.passwordEnv) == 0) && (!has(self.passwordFile) || size(self.passwordFile) == 0))",message="authentication: false cannot be combined with passwordSecretRef, passwordEnv or passwordFile"
type RCONSpec struct {
	// Protocol is the wire protocol the agent speaks to the game's console
	// port. Multiple protocols are supported: "source" is the Valve/Minecraft
	// packet-framed RCON protocol; "telnet" is a raw line-based TCP console
	// (e.g. 7 Days to Die) — send a line, get a line back, no framing;
	// "websocket" is the Rust WebRcon protocol (requires +rcon.web 1);
	// "battleye" is the BattlEye RCon protocol used by DayZ and Arma — UDP,
	// with checksum-framed packets and a mandatory client-side keepalive;
	// "satisfactory" is Satisfactory Dedicated Server's HTTPS function-call
	// API (POST /api/v1 with a JSON "function" body, bearer-token auth
	// after a PasswordLogin call) — not a socket protocol at all, so Port
	// here is a TCP port carrying HTTPS, not a raw console stream.
	// "palworld" is Palworld Dedicated Server's REST admin API (plain HTTP,
	// not HTTPS — GET/POST under /v1/api/..., HTTP Basic auth with
	// username "admin" sent on every request, no token or session) — the
	// official replacement for Palworld's now-deprecated source RCON, and
	// likewise not a socket protocol, so Port is a TCP port carrying HTTP.
	// "nuclearoption" is Nuclear Option's JSON-RPC 2.0 TCP socket protocol.
	// "rest" is a generic HTTP/JSON console API (POST-per-command, bearer-
	// or basic-auth, distinct from the bespoke satisfactory/palworld clients).
	// "cli" is console access over the container's stdin/PTY, enabling
	// agent-driven console commands without requiring an exposed network port.
	// "none" means the game has no usable remote console (see consoleMode:
	// pty for stdin-driven games instead).
	// +kubebuilder:default=source
	// +kubebuilder:validation:Enum=source;telnet;websocket;battleye;satisfactory;palworld;nuclearoption;rest;cli;none
	// +optional
	Protocol string `json:"protocol,omitempty"`

	// Port is the port RCON listens on inside the pod (TCP for source,
	// telnet, websocket, satisfactory, and palworld; UDP for battleye).
	// +optional
	Port int32 `json:"port,omitempty"`

	// PasswordSecretRef references a Secret+key containing the RCON
	// password. If unset, the operator generates a password and stores
	// it in an auto-managed Secret (<gameserver>-rcon, key "password").
	// +optional
	PasswordSecretRef *SecretKeySelector `json:"passwordSecretRef,omitempty"`

	// PasswordEnv is the environment variable the game container reads
	// the RCON password from (e.g. "RCON_PASSWORD" for itzg/minecraft).
	// When set, the operator injects the resolved password into the game
	// container via this env var and mounts the same value for the agent
	// sidecar, so the dashboard console can authenticate. Leave empty for
	// games that take their RCON password some other way.
	// +optional
	PasswordEnv string `json:"passwordEnv,omitempty"`

	// PasswordFile is the path, relative to the game data mount, of a
	// password file the game image manages itself (e.g. "config/rconpw"
	// for factorio). When set, the operator does not generate or mount an
	// RCON Secret and does not inject PasswordEnv; the agent reads the
	// password from this file inside the data volume on every connection.
	// Ignored when PasswordSecretRef is set.
	// +kubebuilder:validation:Pattern=`^([A-Za-z0-9_][A-Za-z0-9_.-]*/)*[A-Za-z0-9_][A-Za-z0-9_.-]*$`
	// +kubebuilder:validation:MaxLength=255
	// +optional
	PasswordFile string `json:"passwordFile,omitempty"`

	// Authentication reports whether the console requires a password. Defaults to true.
	// When false the operator generates no RCON Secret and injects no password; use it
	// for consoles with no authentication, such as Nuclear Option's loopback-only
	// remote-command port.
	// +kubebuilder:default=true
	// +optional
	Authentication *bool `json:"authentication,omitempty"`
}

// AuthenticationEnabled reports whether the RCON console requires a password.
// A nil receiver or unset field means true (the default).
func (r *RCONSpec) AuthenticationEnabled() bool {
	if r == nil || r.Authentication == nil {
		return true
	}
	return *r.Authentication
}

// GameProbesSpec are the default probes for the game container.
type GameProbesSpec struct {
	// +optional
	Readiness *corev1.Probe `json:"readiness,omitempty"`
	// +optional
	Liveness *corev1.Probe `json:"liveness,omitempty"`
	// +optional
	Startup *corev1.Probe `json:"startup,omitempty"`
}

// AgentSpec tunes the Gameplane sidecar that runs alongside the game.
type AgentSpec struct {
	// Image overrides the default agent image. Normally set by the
	// operator to the image matching its own build.
	// +optional
	Image string `json:"image,omitempty"`

	// Resources overrides agent resources.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// ConfigField is a single user-tunable setting surfaced in the wizard.
type ConfigField struct {
	// Name is the field identifier (also used as an env var when
	// Target is "env").
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// DisplayName is shown in the UI.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Description explains the field to end users.
	// +optional
	Description string `json:"description,omitempty"`

	// Type controls the input widget in the wizard. Values of password
	// fields are stored in a per-GameServer Secret and injected via
	// SecretKeyRef, never inline in the pod spec.
	// +kubebuilder:validation:Enum=string;int;bool;enum;password
	// +kubebuilder:default=string
	Type string `json:"type"`

	// Default is the default value (as a string) rendered in the wizard.
	// +optional
	Default string `json:"default,omitempty"`

	// Enum restricts valid values when Type=enum.
	// +optional
	Enum []string `json:"enum,omitempty"`

	// Required, when true, blocks wizard submission if unset.
	// +optional
	Required bool `json:"required,omitempty"`

	// Target controls where the value is applied. "env" (default) sets
	// an env var on the game container; "file" makes the value available
	// to spec.configFiles templates instead of the environment.
	// +kubebuilder:validation:Enum=env;file
	// +kubebuilder:default=env
	// +optional
	Target string `json:"target,omitempty"`

	// AutoFromMemoryLimit, when set, computes this field's value from the
	// game container's effective memory limit whenever neither
	// GameServer.spec.config nor Default provides one: floor(limit ×
	// percent / 100), rendered as whole mebibytes with an "M" suffix
	// (e.g. "6144M"). Lets templates size game memory settings (like a
	// JVM heap) to whatever resources the server was given instead of a
	// static default — an explicit user value always wins, and a template
	// without a memory limit leaves the field unset.
	// +optional
	AutoFromMemoryLimit *AutoFromMemoryLimit `json:"autoFromMemoryLimit,omitempty"`

	// Min is the minimum numeric value for int-typed fields. Bounds are
	// inclusive. Ignored for non-int field types.
	// +optional
	Min *int64 `json:"min,omitempty"`

	// Max is the maximum numeric value for int-typed fields. Bounds are
	// inclusive. Ignored for non-int field types.
	// +optional
	Max *int64 `json:"max,omitempty"`

	// MinLength is the minimum string length for string and password-typed
	// fields. Bounds are inclusive. Ignored for non-string field types.
	// +optional
	MinLength *int32 `json:"minLength,omitempty"`

	// MaxLength is the maximum string length for string and password-typed
	// fields. Bounds are inclusive. Ignored for non-string field types.
	// +optional
	MaxLength *int32 `json:"maxLength,omitempty"`
}

// AutoFromMemoryLimit derives a ConfigField value from the game
// container's memory limit.
type AutoFromMemoryLimit struct {
	// Percent of the memory limit the computed value takes. Values well
	// below 100 leave headroom for non-heap memory — e.g. a JVM heap at
	// 75% of the container limit avoids the OOM kill that a heap at or
	// above the limit invites.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	Percent int32 `json:"percent"`
}

// ConfigFile is a single operator-rendered file on the game's data
// volume.
type ConfigFile struct {
	// Path is where the rendered file lands, relative to
	// Storage.MountPath (e.g. "serverconfig.txt", "cfg/server.cfg").
	// Absolute paths and ".." segments are rejected.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('/') && !self.contains('..')",message="path must be relative to the data mount and must not contain '..'"
	Path string `json:"path"`

	// Template is a Go text/template rendered with `.Values` (every
	// configSchema field name mapped to its resolved value, "" when an
	// optional field is unset) and `.Server` (`.Name`, `.Namespace`).
	// Rendering uses missingkey=error: referencing a key outside the
	// schema fails the GameServer.
	// +kubebuilder:validation:MinLength=1
	Template string `json:"template"`
}

// SecretKeySelector references a key inside a namespaced Secret. Mirrors
// corev1.SecretKeySelector but narrowed to the fields we support.
type SecretKeySelector struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// SecretNameRef references a Secret by name only (no key selection).
// Used when the Secret has a fixed schema with known keys.
type SecretNameRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// GameTemplateStatus is intentionally minimal — templates are mostly
// static configuration. The operator populates ObservedGeneration so
// controllers reading templates can detect spec updates.
type GameTemplateStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// InUseCount is the number of GameServers currently referencing
	// this template.
	// +optional
	InUseCount int32 `json:"inUseCount,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=gtmpl;gametmpl
// +kubebuilder:printcolumn:name="Game",type=string,JSONPath=`.spec.game`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="In Use",type=integer,JSONPath=`.status.inUseCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:subresource:status

// GameTemplate is a cluster-scoped blueprint for a specific game. A
// GameTemplate is distributed as part of a Gameplane module (OCI artifact)
// and instantiated via GameServer.
type GameTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GameTemplateSpec   `json:"spec,omitempty"`
	Status GameTemplateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GameTemplateList is a list of GameTemplates.
type GameTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GameTemplate `json:"items"`
}

// init registers GameTemplate types with the scheme builder.
func init() {
	SchemeBuilder.Register(&GameTemplate{}, &GameTemplateList{})
}
