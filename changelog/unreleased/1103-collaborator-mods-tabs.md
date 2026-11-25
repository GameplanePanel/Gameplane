# Server collaborators see the Mods and Modpacks tabs

A server's collaborators (and owner) without the cluster-wide `templates:read`
permission now see the Mods, Modpacks and Console tabs on servers they work on.
The server's template is read through the server itself for them, so nothing
changes for other users. [PR #1103](https://github.com/GameplanePanel/Gameplane/pull/1103)
