#!/usr/bin/env bash
# Each independent suite gets the full published login budget. Use the
# database clock and leave the security state intact across process restarts.
set -euo pipefail
context=${1:?kind context required}
kubectl --context "$context" -n gameplane-system exec gameplane-postgres-0 -- \
    psql -XAt -v ON_ERROR_STOP=1 -U gameplane -d gameplane -c "
        SELECT pg_sleep(GREATEST(0, COALESCE(max(next_us), 0) / 1000000.0 -
                                 extract(epoch FROM statement_timestamp()))::double precision)
        FROM api_rate_buckets WHERE budget IN ('login_ip', 'login_user')"
