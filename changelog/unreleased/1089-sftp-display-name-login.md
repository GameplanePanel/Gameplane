# SFTP login by display name

SSO users no longer have to type their email address to log in over SFTP: the display name works too, for example `sftp -P 2222 alice@sftp.example.com` or `alice+survival@sftp.example.com`. It works when no other account has the same display name and no account uses it as its username; otherwise use the panel username. [PR #1089](https://github.com/GameplanePanel/Gameplane/pull/1089)
