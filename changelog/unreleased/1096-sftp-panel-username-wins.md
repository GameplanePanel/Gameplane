# SFTP login: a panel username always wins over a chosen SFTP username

At SFTP login a panel username now matches before a chosen SFTP username, so nobody can block another person's login by choosing their email address as an SFTP username before that person's account exists. If another account later takes a chosen SFTP username as its panel username, the user who chose it falls back to their default SFTP username until they choose a new one under **Settings, SFTP**. [PR #1096](https://github.com/GameplanePanel/Gameplane/pull/1096)
