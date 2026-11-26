# Module and component repositories now use master

The GameplanePanel module, website and component repositories renamed their
default branch from `main` to `master`. If a git `ModuleSource` tracks
`https://github.com/GameplanePanel/module.git` with `ref: main`, change it to
`ref: master` (or, better, a release tag such as `v0.5.0`); the default chart
values already pin a tag and need no change. [PR #1120](https://github.com/GameplanePanel/Gameplane/pull/1120)
