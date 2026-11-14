# Concurrent game server wakes

The operator durably reserves fleet wake slots, observes explicit stops and recovers
in-flight starts after a restart. TCP and UDP clients can retry during a cold start without
launching a second copy of the game. [PR #904](https://github.com/GameplanePanel/Gameplane/pull/904)
