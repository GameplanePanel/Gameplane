# SFTP login works for SSO accounts

Accounts created through SSO are named by their email address, and the SFTP login refused every such name before checking the password or key. They can now log in with their panel username, for example `sftp -P 2222 alice@example.com@sftp.example.com`. An SSH key that is not registered no longer drops the connection: the client falls back to the password prompt. The API log now names the cause of each refused SFTP login, for operators. [PR #1086](https://github.com/GameplanePanel/Gameplane/pull/1086)
