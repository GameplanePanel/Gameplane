# Breaking: default git module source moves to the v0.4.1 module tag

The chart's `defaultModuleSource.git.ref` now points at the module repository's
`v0.4.1` tag instead of `v0.4.0`. This only affects installs that set
`defaultModuleSource.type: git`; the default `oci` source is unaffected. A git
source serves one version per module, so these modules change version:
ark-survival-ascended 1.0.9, ark-survival-evolved 2.0.2, minecraft-java 2.12.0,
mount-and-blade-2-bannerlord 1.1.1, nuclear-option 1.1.1, palworld 1.3.0,
project-zomboid 1.0.6, squad 1.1.1 and the-isle 1.1.1. Before upgrading the
chart on a git-source install, repin each installed Module of these games to the
new version, or it goes `Failed` with `VersionUnavailable`. ark-survival-evolved
2.0 is itself a breaking module change; see its module README.
[PR #997](https://github.com/GameplanePanel/Gameplane/pull/997)
