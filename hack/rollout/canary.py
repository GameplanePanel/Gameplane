#!/usr/bin/env python3
"""Admit one GameServer at a time after a restored canary and rollback rehearsal."""

import argparse
import hashlib
import ipaddress
import json
import re
import subprocess
import tempfile
import time
import uuid
from pathlib import Path


class Halt(RuntimeError):
    """The fleet must stay at its current revision."""


def require(ok, reason):
    if not ok:
        raise Halt(reason)


def validate_module(module, digest):
    spec, status = module["spec"], module.get("status", {})
    require(re.fullmatch(r"sha256:[0-9a-f]{64}", digest), "pin an OCI module digest")
    require(status.get("phase") == "Ready" and spec.get("version")
            and spec["version"] == status.get("appliedVersion")
            and status.get("observedGeneration") == module["metadata"]["generation"],
            "the pinned module version has not finished reconciling")
    require(digest == spec.get("digest") == status.get("appliedDigest") == status.get("verifiedDigest")
            and status.get("verifyPolicy") in ("keyed", "keyless"),
            "the installed module digest lacks a matching signature verification")


def validate_restore(backup, restore, source, clone):
    """Require a successful all-data restore into a different server of this fleet."""
    source_uid, clone_uid = source["metadata"]["uid"], clone["metadata"]["uid"]
    namespace = source["metadata"]["namespace"]
    require(source_uid != clone_uid and all(obj["metadata"]["namespace"] == namespace
            for obj in (clone, backup, restore)), "the source and canary must be distinct in one namespace")
    status = backup.get("status", {})
    manifest = status.get("captureManifest") or {}
    require(status.get("phase") == "Succeeded" and status.get("snapshotID")
            and manifest.get("serverUID") == source_uid and manifest.get("formatVersion") == 2
            and manifest.get("volumePolicy") == "all-data"
            and manifest.get("quiesceOutcome") == "already-stopped", "a stopped all-data backup is required")
    volumes = manifest.get("volumes", [])
    require(any(v.get("included") and v.get("classification") == "data" for v in volumes)
            and all(v.get("included") or v.get("classification") == "rebuildable" for v in volumes),
            "the capture omits persistent data or unclassified content")
    rs = restore.get("status", {})
    plan = rs.get("recoveryPlan") or {}
    require(rs.get("phase") == "Succeeded" and rs.get("snapshotID") == status["snapshotID"]
            and plan.get("backupUID") == backup["metadata"]["uid"] and plan.get("targetUID") == clone_uid
            and restore["spec"]["serverRef"]["name"] == clone["metadata"]["name"]
            and restore["spec"]["backupRef"]["name"] == backup["metadata"]["name"]
            and manifest.get("templateRevision") and plan.get("templateRevision") == manifest["templateRevision"],
            "the canary has no matching completed restore")
    require(backup["spec"]["serverRef"]["name"] == source["metadata"]["name"]
            and all(v.get("claimUID") for v in volumes if v["included"])
            and plan.get("manifestDigest"), "missing backup or volume provenance")
    target_claims = {v["claimName"]: v["claimUID"] for v in plan.get("volumes", []) if v["included"]}
    require(target_claims and all(target_claims.values()), "missing restored claim identities")
    return {"backupUID": plan["backupUID"], "restoreUID": restore["metadata"]["uid"],
            "snapshotID": status["snapshotID"], "manifestDigest": plan["manifestDigest"],
            "coverage": volumes, "restoredClaims": target_claims}


def command(args, *, stdin=None, timeout=60):
    result = subprocess.run(args, input=stdin, capture_output=True, text=True,
                            timeout=timeout, check=False)
    if result.returncode:
        raise Halt(f"{args[0]} exited {result.returncode}: {result.stderr.strip()[:500]}")
    return result.stdout.strip()


def owned(obj, kind, uid):
    return any(owner.get("controller") and owner.get("kind") == kind and owner.get("uid") == uid
               for owner in obj["metadata"].get("ownerReferences", []))


class Cluster:
    def __init__(self, plan, args):
        self.plan, self.args = plan, args
        self.k = ["kubectl", "--context", args.context, "-n", plan["namespace"]]
        self.timeout = args.timeout

    def get(self, resource, name):
        resource = {"backup": "backups.gameplane.local", "gametemplate": "gametemplates.gameplane.local",
                    "gameserver": "gameservers.gameplane.local"}.get(resource, resource)
        return json.loads(command([*self.k, "get", resource, name, "-o", "json"]))

    def patch(self, gs, fields):
        ops = [{"op": "test", "path": "/metadata/resourceVersion", "value": gs["metadata"]["resourceVersion"]}]
        ops += [{"op": "add", "path": path, "value": value} for path, value in fields.items()]
        command([*self.k, "patch", "gameservers.gameplane.local", gs["metadata"]["name"], "--type=json",
                 "-p", json.dumps(ops)])

    def pins(self, lane):
        pins = self.plan[lane]
        require(re.fullmatch(r"[0-9a-f]{64}", pins["revision"]), "invalid template revision")
        require(re.fullmatch(r"[^\s@]+@sha256:[0-9a-f]{64}", pins["image"]), "pin a game image digest")
        module = self.get("modules.gameplane.local", self.plan["module"])
        validate_module(module, pins["bundle"])
        template = self.get("gametemplate", self.plan["module"])
        require(module["status"].get("appliedTemplate") == template["metadata"]["name"]
                and owned(template, "Module", module["metadata"]["uid"]),
                "the installed template is not owned by the verified module")
        command(["cosign", "verify", "--key", self.args.image_key, pins["image"]], timeout=120)
        return template["spec"]

    def server(self, name):
        obj = self.get("gameserver", name)
        require(obj["spec"]["templateRef"]["name"] == self.plan["module"]
                and obj["spec"].get("templateRollout", {}).get("policy") == "OnRequest",
                f"{name} must use this module and OnRequest")
        return obj

    def inventory(self):
        all_servers = json.loads(command([*self.k[:3], "get", "gameservers.gameplane.local", "-A", "-o", "json"]))["items"]
        fleet = [gs for gs in all_servers if gs["spec"]["templateRef"]["name"] == self.plan["module"]]
        require(all(gs["metadata"]["namespace"] == self.plan["namespace"] for gs in fleet),
                "a server outside the rollout namespace shares this cluster-scoped template")
        return {gs["metadata"]["name"]: gs["metadata"]["uid"] for gs in fleet}

    def claims(self, server):
        all_claims = json.loads(command([*self.k, "get", "pvc", "-o", "json"]))["items"]
        result = {p["metadata"]["name"]: p["metadata"]["uid"] for p in all_claims
                  if owned(p, "GameServer", server["metadata"]["uid"])}
        require(server["metadata"]["name"] + "-data" in result, "primary owned PVC missing")
        return result

    def wait_server(self, name, uid, revision, suspended=False):
        deadline = time.monotonic() + self.timeout
        while time.monotonic() < deadline:
            gs = self.server(name)
            require(gs["metadata"]["uid"] == uid, f"{name} was replaced")
            rev = gs.get("status", {}).get("templateRevision", {})
            if rev.get("effective") == revision and rev.get("desired") == revision:
                if suspended and gs["spec"].get("suspend") and not rev.get("running"):
                    pod = command([*self.k, "get", "pod", name+"-0", "--ignore-not-found", "-o", "name"])
                    if not pod:
                        return gs
                if not suspended and rev.get("running") == revision:
                    return gs
            time.sleep(2)
        raise Halt(f"{name} did not reach revision {revision} within {self.timeout}s")

    def pod(self, gs, revision, image):
        name = gs["metadata"]["name"]
        sts = self.get("statefulset", name)
        pod = self.get("pod", name+"-0")
        require(owned(sts, "GameServer", gs["metadata"]["uid"])
                and owned(pod, "StatefulSet", sts["metadata"]["uid"])
                and pod["metadata"].get("annotations", {}).get("gameplane.local/template-revision") == revision
                and any(c["name"] == "game" and c["image"] == image for c in pod["spec"]["containers"])
                and any(c["name"] == "game" and c.get("ready") and c.get("imageID")
                        for c in pod["status"].get("containerStatuses", [])), "runtime identity or readiness mismatch")
        return pod

    def client_join(self, address):
        result = subprocess.run([self.args.join, "-game", self.plan["game"], "-addr", address,
                                 "-deadline", "30s"], capture_output=True, text=True, timeout=45)
        parts = result.stdout.strip().split("\t", 3)
        verdict = " ".join(parts[:3])[:120]
        require(result.returncode == 0 and parts[:3] == ["VERDICT", "PASS", "JOINED"]
                and len(parts) == 4 and parts[3].strip(), f"game client did not join ({verdict})")
        return parts[3]

    def join(self, pod):
        protocol = self.plan.get("protocol", "TCP")
        port = self.plan["port"]
        game = next(c for c in pod["spec"]["containers"] if c["name"] == "game")
        require(any(p["containerPort"] == port and p.get("protocol", "TCP") == protocol
                    for p in game.get("ports", [])), "planned game port and protocol absent from game container")
        if protocol == "UDP":
            # kubectl has no UDP port-forward. An in-cluster or routed runner
            # addresses this exact canary, including on IPv6-only clusters.
            ip = ipaddress.ip_address(pod["status"].get("podIP", ""))
            return self.client_join(f"[{ip}]:{port}" if ip.version == 6 else f"{ip}:{port}")
        # kubectl binds an ephemeral local port and holds this exact pod as the
        # destination. The fleet's public address could point at a different game.
        with tempfile.TemporaryFile(mode="w+") as log:
            proc = subprocess.Popen([*self.k, "port-forward", "--address=127.0.0.1",
                                     "pod/"+pod["metadata"]["name"], ":"+str(port)], stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + min(30, self.timeout)
                while time.monotonic() < deadline:
                    log.seek(0)
                    match = re.search(r"Forwarding from 127\.0\.0\.1:(\d+)", log.read())
                    if match:
                        return self.client_join("127.0.0.1:"+match[1])
                    require(proc.poll() is None, "port-forward exited before client join")
                    time.sleep(.15)
                raise Halt("port-forward did not publish a local port")
            finally:
                proc.terminate()
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait(timeout=10)

    def probe(self, target, gs, pins):
        command([*self.k, "wait", "--for=condition=Ready", "pod/"+gs["metadata"]["name"]+"-0",
                 f"--timeout={self.timeout}s"], timeout=self.timeout+10)
        first = self.pod(gs, pins["revision"], pins["image"])
        pod = first["metadata"]["name"]
        def runtime(obj):
            game = next(c for c in obj["status"]["containerStatuses"] if c["name"] == "game")
            require(game.get("containerID") and game.get("imageID"), "missing runtime container identity")
            return (obj["metadata"]["uid"], game["containerID"], game["imageID"], game.get("restartCount", 0))
        initial = runtime(first)
        evidence = self.join(first)
        checks = target["checks"]
        claims = self.claims(gs)
        require({c["claim"] for c in checks} == set(claims), "supply a marker check for every retained PVC")
        markers = {}
        for check in [*checks, self.plan["save"]]:
            argv, expected = check["argv"], check["expected"]
            require(isinstance(argv, list) and all(isinstance(a, str) for a in argv) and argv
                    and isinstance(expected, str) and expected.strip(), "invalid marker/save assertion")
            result = command([*self.k, "exec", "pod/"+pod, "-c", "game", "--", *argv])
            require(result == expected, f"{pod} failed its {check.get('claim', 'save')} assertion")
            if "claim" in check:
                markers[check["claim"]] = expected
        last = self.pod(self.server(gs["metadata"]["name"]), pins["revision"], pins["image"])
        require(initial == runtime(last) and claims == self.claims(gs),
                "pod or PVC identity changed during the probe")
        return {"serverUID": gs["metadata"]["uid"], "podUID": initial[0], "containerID": initial[1],
                "runtimeImageID": initial[2], "restartCount": initial[3], "pvcs": claims,
                "image": pins["image"], "revision": pins["revision"], "join": evidence,
                "markers": markers, "save": self.plan["save"]["expected"]}

    def restore(self, clone, backup, name):
        resource = {"apiVersion": "gameplane.local/v1alpha1", "kind": "Restore",
                    "metadata": {"name": name, "namespace": self.plan["namespace"]},
                    "spec": {"backupRef": {"name": backup}, "serverRef": {"name": clone}}}
        existing = command([*self.k, "get", "restores.gameplane.local", name, "--ignore-not-found", "-o", "name"])
        if not existing:
            command([*self.k, "create", "-f", "-"], stdin=json.dumps(resource))
        deadline = time.monotonic()+self.timeout
        while time.monotonic() < deadline:
            restore = self.get("restores.gameplane.local", name)
            require(restore["spec"] == resource["spec"], "restore name already belongs to a different request")
            phase = restore.get("status", {}).get("phase")
            require(phase != "Failed", "restoring baseline data failed")
            if phase == "Succeeded":
                return restore
            time.sleep(2)
        raise Halt("clone restore exceeded deadline")


class Journal:
    """CAS replacement serializes operators without owning GameServer lifecycle."""

    def __init__(self, cluster, digest, recover):
        self.k = cluster.k
        self.name = "gp-rollout-" + hashlib.sha256(cluster.plan["module"].encode()).hexdigest()[:24]
        self.owner = str(uuid.uuid4())
        raw = command([*self.k, "get", "configmap", self.name, "--ignore-not-found", "-o", "json"])
        if raw:
            self.cm = json.loads(raw)
            self.data = json.loads(self.cm["data"]["progress"])
            require(self.data["plan"] == digest, "another rollout plan occupies this journal")
            require(not self.data.get("owner") or self.data["owner"] == recover,
                    "journal is held by another invocation; inspect its owner before recovery")
        else:
            self.cm = {"apiVersion": "v1", "kind": "ConfigMap",
                       "metadata": {"name": self.name, "namespace": cluster.plan["namespace"]}}
            self.data = {"plan": digest, "receipts": {}}
        self.data["owner"] = self.owner
        self.write(create=not raw)

    def write(self, create=False):
        self.cm["data"] = {"progress": json.dumps(self.data, separators=(",", ":"))}
        self.cm = json.loads(command([*self.k, "create" if create else "replace", "-f", "-", "-o", "json"],
                                     stdin=json.dumps(self.cm)))

    def release(self, error=""):
        self.data.update(owner="", error=error[:500])
        self.write()


def selected_image(template, server):
    spec = server["spec"]
    if spec.get("image"):
        return spec["image"]
    versions = template.get("versions", [])
    if not versions:
        return template["image"]
    if spec.get("version"):
        selected = next((v for v in versions if v["id"] == spec["version"]), None)
        require(selected, "the server's selected version is missing from the template")
    else:
        selected = next((v for v in versions if v.get("default")), versions[0])
    return selected["image"]


def admit(cluster, journal, target, lane, label, provenance=None):
    p, data = cluster.plan, journal.data
    name, pins = target["name"], p[lane]
    template = cluster.pins(lane)
    gs = cluster.server(name)
    rev = gs.get("status", {}).get("templateRevision", {})
    require(rev.get("desired") == pins["revision"] and selected_image(template, gs) == pins["image"],
            f"{name}: desired revision or resolved game image differs from the signed plan")
    prior = p["baseline" if lane == "candidate" else "candidate"]["revision"]
    fresh = data.get("active", {}).get("label") != label
    if fresh:
        require(not data.get("active"), "another target is unfinished")
        require(not gs["spec"].get("suspend") and rev.get("effective") == rev.get("running") == prior
                and gs["spec"]["templateRollout"].get("requestedRevision") != pins["revision"],
                f"{name} is not running the expected previous revision")
        data["active"] = {"label": label, "name": name, "serverUID": gs["metadata"]["uid"],
                          "pvcs": cluster.claims(gs), "from": prior, "to": pins["revision"]}
        journal.write()
    active = data["active"]
    require(active["serverUID"] == gs["metadata"]["uid"] and active["pvcs"] == cluster.claims(gs),
            f"{name}: server or claim replaced during admission")
    restore_required = lane == "baseline" and p["rollback"] == "restore-required"
    patch = {"/spec/templateRollout/requestedRevision": pins["revision"]}
    if restore_required:
        patch["/spec/suspend"] = True
    journal.write()  # Check the current resourceVersion before each admission.
    cluster.patch(gs, patch)
    if restore_required:
        gs = cluster.wait_server(name, active["serverUID"], pins["revision"], suspended=True)
        restore = cluster.restore(name, p["backup"], "gp-rollback-"+data["plan"][:20])
        source, backup = cluster.server(p["source"]), cluster.get("backup", p["backup"])
        restored = validate_restore(backup, restore, source, gs)
        require(restored["snapshotID"] == provenance["snapshotID"], "rollback restored another snapshot")
        journal.write()
        cluster.patch(cluster.server(name), {"/spec/suspend": False})
        active["rollbackRestoreUID"] = restored["restoreUID"]
    gs = cluster.wait_server(name, active["serverUID"], pins["revision"])
    receipt = cluster.probe(target, gs, pins)
    require(receipt["pvcs"] == active["pvcs"], "workload changed its PVC set during rollout")
    data["receipts"][label] = {**active, **receipt}
    del data["active"]
    journal.write()


def run_phase(cluster, journal, phase):
    p, data = cluster.plan, journal.data
    receipts, canary = data["receipts"], p["canary"]
    backup, restore = cluster.get("backup", p["backup"]), cluster.get("restores.gameplane.local", p["restore"])
    source, clone = cluster.server(p["source"]), cluster.server(canary["name"])
    inventory = cluster.inventory()
    require(set(inventory) == {p["source"], canary["name"], *(t["name"] for t in p["targets"])},
            "the dedicated module has an unexpected or omitted server in this namespace")
    for name in inventory:
        cluster.server(name)
    if phase == "preflight":
        require(not receipts and not data.get("active"), "preflight cannot replace an ongoing rehearsal")
        template = cluster.pins("baseline")
        for name in inventory:
            gs = cluster.server(name)
            rev = gs.get("status", {}).get("templateRevision", {})
            require(rev.get("effective") == rev.get("running") == rev.get("desired") == p["baseline"]["revision"]
                    and selected_image(template, gs) == p["baseline"]["image"]
                    and gs["spec"]["templateRollout"].get("requestedRevision") != p["candidate"]["revision"]
                    and not gs["spec"].get("suspend"), f"{name}: expected an unapproved candidate and running baseline")
        data["inventory"] = inventory
    else:
        require(inventory == data.get("inventory"), "preflight missing or fleet identities changed")
    provenance = validate_restore(backup, restore, source, clone)
    require(backup["status"]["captureManifest"]["templateRevision"] == p["baseline"]["revision"],
            "backup was made with another baseline revision")
    live_claims = cluster.claims(clone)
    require(all(live_claims.get(name) == uid for name, uid in provenance["restoredClaims"].items()),
            "restored canary claims were replaced")
    require(not data.get("origin") or provenance == data["origin"], "canary restore provenance changed")
    data["origin"] = provenance
    if phase == "preflight":
        journal.write()
        return
    if phase == "canary":
        if "candidate-canary" not in receipts:
            # Check the actual restored save under its previous engine before
            # admitting the first potentially format-changing candidate.
            if "restored-baseline" not in receipts:
                receipts["restored-baseline"] = cluster.probe(canary, clone, p["baseline"])
                journal.write()
            admit(cluster, journal, canary, "candidate", "candidate-canary")
    elif phase == "rollback":
        require("candidate-canary" in receipts, "qualify the candidate on the restored canary first")
        if "rollback-canary" not in receipts:
            admit(cluster, journal, canary, "baseline", "rollback-canary", provenance)
    else:
        require("rollback-canary" in receipts, "complete the rollback rehearsal before promotion")
        for label in ("restored-baseline", "candidate-canary", "rollback-canary"):
            require(receipts[label]["serverUID"] == clone["metadata"]["uid"]
                    and receipts[label]["pvcs"] == cluster.claims(clone), "rehearsed canary was replaced")
        if "requalified-canary" not in receipts:
            admit(cluster, journal, canary, "candidate", "requalified-canary")
        else:
            # A paused fleet may resume days later. Recheck the live canary
            # before advancing to the next target.
            cluster.pins("candidate")
            current = cluster.probe(canary, cluster.server(canary["name"]), p["candidate"])
            require(current["pvcs"] == receipts["requalified-canary"]["pvcs"], "canary claims changed")
        for target in p["targets"]:
            label = "fleet/"+target["name"]
            if label not in receipts:
                admit(cluster, journal, target, "candidate", label)
        data["complete"] = True
    journal.write()


def validate_plan(plan):
    target_names = [item["name"] for item in plan["targets"]]
    names = [plan["canary"]["name"], *target_names]
    require(target_names and len(names) == len(set(names)) and plan["source"] != names[0],
            "select a distinct canary and unique fleet targets")
    require(plan["rollback"] in ("compatible", "restore-required")
            and 0 < plan["port"] <= 65535, "invalid rollback mode or game port")
    require(plan.get("protocol", "TCP") in ("TCP", "UDP"), "unsupported game transport")
    require(plan["baseline"]["revision"] != plan["candidate"]["revision"], "baseline and candidate coincide")
    for name in [*names, plan["source"], plan["namespace"], plan["module"], plan["backup"], plan["restore"]]:
        require(re.fullmatch("[a-z0-9](?:[-.a-z0-9]*[a-z0-9])?", name), "invalid Kubernetes resource name")


def main():
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument("phase", choices=["preflight", "canary", "rollback", "promote"])
    cli.add_argument("plan", type=Path)
    cli.add_argument("--context", required=True)
    cli.add_argument("--image-key", required=True, help="trusted local cosign public key for game images")
    cli.add_argument("--join", required=True, help="join-once or a trusted game-specific adapter with its contract")
    cli.add_argument("--timeout", type=int, default=600)
    cli.add_argument("--recover-owner", help="exact journal owner after the previous process has exited")
    args = cli.parse_args()
    plan = json.loads(args.plan.read_text())
    validate_plan(plan)
    require(0 < args.timeout <= 3600, "choose a timeout between 1s and 1h")
    digest = hashlib.sha256(json.dumps(plan, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    cluster = Cluster(plan, args)
    journal = Journal(cluster, digest, args.recover_owner)
    try:
        run_phase(cluster, journal, args.phase)
    except Exception as exc:
        journal.release(str(exc))
        raise
    journal.release()
    print(json.dumps(journal.data, indent=2))


if __name__ == "__main__":
    try:
        main()
    except (Halt, ValueError, OSError, KeyError, subprocess.TimeoutExpired) as exc:
        raise SystemExit(str(exc)) from exc
