# Pod-local game probes

GameServer and GameTemplate HTTP and TCP probes now reject an explicit host. Remove the host
from existing probes so the kubelet connects to the game Pod before its next rollout. [PR #889](https://github.com/GameplanePanel/Gameplane/pull/889)
