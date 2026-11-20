# Share links distinguish stopped and sleeping servers

Public share links show manually stopped servers as stopped and explain that the owner must start them. Shared start actions wake idle-asleep servers without overriding a manual stop, and the page keeps observing an accepted wake until the server starts or a five-minute wait times out. View-only links no longer offer Start server, and a manual stop that races a wake request shows the same owner-must-start message. [PR #1019](https://github.com/GameplanePanel/Gameplane/pull/1019)
