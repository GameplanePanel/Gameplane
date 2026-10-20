#!/usr/bin/env python3
"""Generate a JSON Schema for module template.yaml files from the GameTemplate CRD.

template.yaml in a Gameplane module IS a GameTemplate custom resource, so its
schema is the CRD's openAPIV3Schema. Editors (redhat.vscode-yaml) otherwise
guess a schema from their schema store and mis-flag valid fields; a
`# yaml-language-server: $schema=` modeline in each template.yaml points at the
file this writes.

Output goes into the GameplanePanel/module submodule so it lives with the files it
validates (and resolves for a standalone checkout of that repo too). Re-run
after CRD changes:  make module-schema

The module.yaml schema is hand-maintained (small, stable manifest) at the same
location and is NOT generated here.
"""
import json
import sys
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parent.parent
CRD = REPO / "charts/gameplane/crd-manifests/gameplane.local_gametemplates.yaml"
OUT = REPO / "modules/.schema/gametemplate.schema.json"


def main() -> int:
    if not CRD.exists():
        print(f"CRD not found: {CRD}", file=sys.stderr)
        return 1
    crd = yaml.safe_load(CRD.read_text())
    versions = crd["spec"]["versions"]
    # Single served version today; take the storage version to be safe.
    ver = next((v for v in versions if v.get("storage")), versions[0])
    schema = ver["schema"]["openAPIV3Schema"]

    # JSON Schema envelope. The CRD's openAPIV3Schema already describes the whole
    # document (apiVersion/kind/metadata/spec/status); we only add the draft
    # marker + a title and pin apiVersion/kind so a wrong header is flagged.
    schema["$schema"] = "http://json-schema.org/draft-07/schema#"
    schema["title"] = "Gameplane GameTemplate (module template.yaml)"
    schema.setdefault("properties", {})
    schema["properties"]["apiVersion"] = {
        "const": f"{crd['spec']['group']}/{ver['name']}",
        "description": "gameplane.local/v1alpha1",
    }
    schema["properties"]["kind"] = {
        "const": crd["spec"]["names"]["kind"],
        "description": "GameTemplate",
    }

    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(schema, indent=2, sort_keys=False) + "\n")
    print(f"wrote {OUT.relative_to(REPO)} ({OUT.stat().st_size} bytes)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
