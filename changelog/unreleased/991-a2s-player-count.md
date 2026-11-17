# Player counts from the Steam server query

A module can read its player count over Valve's A2S query instead of the
console, with `capabilities.players.query` (`protocol: a2s` and the UDP query
port). Games without a console that lists players, such as Valheim, which has
no RCON, can then show a player count and sleep when empty.
[PR #991](https://github.com/GameplanePanel/Gameplane/pull/991)
