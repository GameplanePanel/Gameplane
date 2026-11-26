# Breaking: git module sources default to master

A git `ModuleSource` with no `spec.git.ref`, or an empty one, now indexes
`master` instead of `main`, matching the GameplanePanel repositories' renamed
default branch. Sources created with the old CRD default keep `ref: main`
stored and are unaffected; a source stored with `ref: ""` now resolves to
`master`, so set `ref: main` on it if its repository still uses `main`. For the
chart's default source, the shipped values pin `defaultModuleSource.git.ref` to
a release tag; if you cleared it, the chart now renders `master`, so set
`defaultModuleSource.git.ref: main` in your Helm values instead of editing the
resource. [PR #1121](https://github.com/GameplanePanel/Gameplane/pull/1121)
