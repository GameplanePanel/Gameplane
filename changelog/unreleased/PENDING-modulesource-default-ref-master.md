# Breaking: git module sources default to master

A git `ModuleSource` with no `spec.git.ref` now indexes `master` instead of
`main`, matching the GameplanePanel repositories' renamed default branch. The
chart's `defaultModuleSource.git.ref` falls back to `master` the same way. If a
git source points at a repository whose branch is still `main`, set
`ref: main` on it before upgrading. Existing ModuleSources keep the ref the API
server stored when they were created.
