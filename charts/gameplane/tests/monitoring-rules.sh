#!/usr/bin/env bash
# Render and evaluate shipped rules in CI, using the same Prometheus as E2E.
set -euo pipefail
cd "$(dirname "$0")/../../.."
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
helm template gameplane charts/gameplane -f docs/examples/monitoring/gameplane-values.yaml \
  --set 'operator.gameNamespaces={tenant-b,gameplane-games}' > "$scratch/rendered.yaml"
python3 - "$scratch" <<'PY'
import pathlib,sys,yaml
root=pathlib.Path(sys.argv[1])
groups=[]
monitors={}
for document in yaml.safe_load_all((root/'rendered.yaml').read_text()):
    if document and document.get('kind')=='PrometheusRule':
        groups.extend(document['spec']['groups'])
    if document and document.get('kind')=='PodMonitor':
        monitors[document['metadata']['name']]=document['spec']['namespaceSelector']['matchNames']
expected=['gameplane-games','tenant-b']
assert monitors=={'gameplane-agent':expected,'gameplane-tunnel':expected}, monitors
(root/'rules.yaml').write_text(yaml.safe_dump({'groups':groups}))
PY
cp charts/gameplane/tests/monitoring-rules.test.yaml "$scratch/tests.yaml"
# mktemp creates a private directory, but promtool runs as the image's nobody
# user. These generated fixtures contain no credentials and are mounted read-only.
chmod 755 "$scratch"
chmod 644 "$scratch/rules.yaml" "$scratch/tests.yaml"
docker run --rm --volume "$scratch:/work:ro" --workdir /work --entrypoint /bin/promtool \
  prom/prometheus:v3.13.4 test rules tests.yaml
