# Server collaborators see the Mods and Modpacks tabs

A server's collaborators (and owner) no longer need the cluster-wide
`templates:read` permission to see the Mods, Modpacks and Console tabs on the
servers they work on. As before, each tab still appears only when the server's
template and active version support it. The server's template is read through
the server itself for them, so nothing changes for other users.
[PR #1103](https://github.com/GameplanePanel/Gameplane/pull/1103)
