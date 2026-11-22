# Unreleased changelog entries

Every pull request with a change that users, operators or module authors will
notice adds one Markdown file here. One file per pull request means two pull
requests never edit the same lines, so entries never conflict.

When a version is released, the maintainer folds these files into a new
section of [`CHANGELOG.md`](../../CHANGELOG.md) and deletes them. Only
`CHANGELOG.md` reaches the GitHub release notes.

See [Changelog entries](../../docs/contributing.md#changelog-entries) for when
an entry is needed and how to write it.

## File name

`<PR number>-<short-slug>.md`, for example `930-detect-apiserver-endpoint.md`.
If you do not know the PR number yet, use the issue number or a slug alone and
rename the file once the PR is open.

## Template

```markdown
# Short title of the change

What changed, in one to four plain sentences, written for the person who runs
Gameplane. Say what they must do before or after upgrading, if anything, and
link the docs that explain it. [PR #NNN](https://github.com/GameplanePanel/Gameplane/pull/NNN)
```

Start the title with `Breaking:` when an upgrade needs a manual step or a
setting, API, CRD field or chart value stops working as before.
