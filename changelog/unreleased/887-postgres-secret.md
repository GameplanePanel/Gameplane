# PostgreSQL connection secrets

The API and `bootstrap-admin` can read the database connection string from an existing
Kubernetes Secret. Set `api.db.existingSecret` and `api.db.secretKey` to keep the DSN out of
Helm values. [PR #887](https://github.com/GameplanePanel/Gameplane/pull/887)
