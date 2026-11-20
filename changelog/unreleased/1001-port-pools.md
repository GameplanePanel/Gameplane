# Port pools for servers behind one public address

`operator.portPools` defines ranges of ports on a shared endpoint, such as a
relay or an edge that forwards ports, and the templates whose servers draw
from each. Every such server gets a contiguous block of Service ports of its
own, kept for its life, even when the pool later shrinks, and shown by the new
`PortAllocation` condition. Servers whose games send no hostname no longer
need their ports set by hand behind one address. A full pool never takes
ports from a server that has them: new servers wait, and template updates
that need more ports wait on the current version. See
[Port pools](../../docs/networking.md#port-pools).
[PR #1001](https://github.com/GameplanePanel/Gameplane/pull/1001)
