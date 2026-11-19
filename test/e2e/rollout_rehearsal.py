"""Kind-only exercise of durable rollout and real PVC/Restic behavior.

The HTTP fixture models on-disk format compatibility. Real game joins and
registry signature verification have dedicated Minecraft and module E2Es.
"""
import hashlib
import json
import sys
import time
from pathlib import Path
from types import SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "hack/rollout"))
from canary import Cluster, Halt, Journal, command, require, run_phase


class Fixture(Cluster):
    def pins(self, lane):
        template = self.get("gametemplate", self.plan["module"])["spec"]
        require(self.plan[lane]["image"] == template["image"], "unexpected test fixture image")
        return template

    def join(self, pod):
        if pod["metadata"]["annotations"]["gameplane.local/template-revision"] == self.reject_join_revision:
            return super().join(pod)
        value = command([*self.k, "exec", pod["metadata"]["name"], "-c", "game", "--", "wget", "-qO-",
                         "http://127.0.0.1:25565/marker"])
        require(value == "world-a", "fixture cannot serve the restored world")
        return "fixture HTTP read of restored world"

    def create(self, kind, name, spec):
        self.resources.append((kind.lower()+"s.gameplane.local", name))
        command([*self.k, "create", "-f", "-"], stdin=json.dumps({
            "apiVersion": "gameplane.local/v1alpha1", "kind": kind,
            "metadata": {"name": name}, "spec": spec}))

    def phase(self, kind, name):
        deadline = time.monotonic()+self.timeout
        while time.monotonic() < deadline:
            obj = self.get(kind, name)
            phase = obj.get("status", {}).get("phase")
            require(phase != "Failed", f"{kind}/{name}: {obj.get('status')}")
            if phase == "Succeeded":
                return obj
            time.sleep(2)
        raise Halt(f"{kind}/{name} timed out")

    def installed(self, spec):
        name = self.plan["module"]
        # The fixture exclusively controls spec. Avoid racing status writes,
        # and replace the whole spec so rollback removes candidate-only fields.
        patch = [{"op": "replace", "path": "/spec", "value": spec}]
        command([*self.k, "patch", "gametemplates.gameplane.local", name, "--type=json", "-p", json.dumps(patch)])
        deadline = time.monotonic()+self.timeout
        while time.monotonic() < deadline:
            # Template-controller status is QA-independent. Read the actual hash
            # the GameServer reconciler computed from this installation.
            gs = self.server(self.plan["source"])
            current = gs.get("status", {}).get("templateRevision", {}).get("desired")
            clone = self.server(self.plan["canary"]["name"])
            clone_desired = clone.get("status", {}).get("templateRevision", {}).get("desired")
            if current and current != self.previous_desired and current == clone_desired:
                self.previous_desired = current
                return {"revision": current, "image": spec["image"]}
            time.sleep(2)
        raise Halt("server did not observe the new installed template")

    def baseline_ready(self, name, revision):
        gs = self.server(name)
        self.wait_server(name, gs["metadata"]["uid"], revision)
        command([*self.k, "wait", "--for=condition=Ready", "pod/"+name+"-0", "--timeout=180s"], timeout=190)

    def read(self, name, *cmd):
        return command([*self.k, "exec", name+"-0", "-c", "game", "--", *cmd])

def target(cluster, name):
    pod = cluster.get("pod", name+"-0")
    volumes = {v["name"]: v["persistentVolumeClaim"]["claimName"] for v in pod["spec"]["volumes"]
               if "persistentVolumeClaim" in v}
    game = next(c for c in pod["spec"]["containers"] if c["name"] == "game")
    checks = []
    for mount in game["volumeMounts"]:
        if mount["name"] not in volumes:
            continue
        path = mount["mountPath"]
        if path == "/mods":
            # The rebuildable mod package differs on the two servers. Restic
            # must preserve the clone's installed file across both restores.
            expected = "source-package" if name == cluster.plan["source"] else "clone-package"
            file = path+"/sample.pk3"
        else:
            expected = {"/data": "world-a", "/extra": "extra-a"}[path]
            file = path+"/marker"
        checks.append({"claim": volumes[mount["name"]], "argv": ["cat", file], "expected": expected})
    require(len(checks) == 3, "missing fixture claims")
    return {"name": name, "checks": checks}


def run(context, prefix, join):
    require(context.startswith("kind-") and prefix.startswith("rollout-"), "disposable kind fixture required")
    source, clone = prefix+"-source", prefix+"-clone"
    plan = {"namespace": "gameplane-games", "module": prefix, "source": source, "backup": prefix+"-backup",
            "restore": prefix+"-restore", "game": "minecraft-java", "port": 25565,
            "save": {"argv": ["sh", "-ec", "sync; printf saved"], "expected": "saved"}}
    cluster = Fixture(plan, SimpleNamespace(context=context, timeout=240, join=join))
    cluster.resources, cluster.previous_desired, cluster.reject_join_revision = [], None, None
    serve = "exec httpd -f -p 25565 -h /data"
    base = {"displayName": prefix, "game": "rollout-fixture", "version": "1", "image": "busybox:1.36",
            "command": ["sh", "-ec", "test ! -e /data/format2; "+serve],
            "ports": [{"name": "game", "containerPort": 25565, "protocol": "TCP", "advertise": True}],
            "security": {"runAsUser": 65532, "runAsGroup": 65532, "fsGroup": 65532},
            "storage": {"size": "1Gi", "mountPath": "/data", "extra": [
                {"name": "world", "mountPath": "/extra", "size": "1Gi", "backupRole": "data"},
                {"name": "packages", "mountPath": "/mods", "size": "1Gi", "backupRole": "rebuildable"}]}}
    try:
        cluster.create("GameTemplate", prefix, base)
        for name in (source, clone):
            cluster.create("GameServer", name, {"templateRef": {"name": prefix}, "templateRollout": {"policy": "OnRequest"}})
        deadline = time.monotonic()+cluster.timeout
        while time.monotonic() < deadline:
            rev = cluster.server(source).get("status", {}).get("templateRevision", {}).get("running")
            if rev:
                break
            time.sleep(2)
        require(rev, "missing initial running revision")
        cluster.previous_desired = rev
        plan["baseline"] = {"revision": rev, "image": base["image"]}
        for name in (source, clone):
            cluster.baseline_ready(name, rev)
            cluster.read(name, "sh", "-ec", "echo world-a > /data/marker; echo extra-a > /extra/marker; "
                         +"echo "+("source-package" if name == source else "clone-package")+" > /mods/sample.pk3; sync")
        cluster.create("Backup", plan["backup"], {"serverRef": {"name": source},
                       "repoRef": {"name": "e2e-restic-creds", "key": "repo"},
                       "powerPolicy": "stop-copy-restart", "volumePolicy": "all-data", "quiesce": False})
        cluster.phase("backup", plan["backup"])
        # Source and target must demonstrably differ before restoring.
        cluster.read(clone, "sh", "-ec", "echo corrupt > /data/marker; echo corrupt > /extra/marker; sync")
        cluster.create("Restore", plan["restore"], {"backupRef": {"name": plan["backup"]}, "serverRef": {"name": clone}})
        cluster.phase("restores.gameplane.local", plan["restore"])
        for name in (source, clone):
            cluster.baseline_ready(name, rev)
        plan["canary"], plan["targets"] = target(cluster, clone), [target(cluster, source)]
        held_pod = cluster.get("pod", source+"-0")["metadata"]["uid"]
        for scenario in ("compatible", "restore-required", "bad-join", "bad-image"):
            candidate = {**base, "version": scenario}
            if scenario == "restore-required":
                candidate["command"] = ["sh", "-ec", "echo migrated > /data/format2; "+serve]
            if scenario == "bad-image":
                candidate["image"] = "127.0.0.1:1/no-image@sha256:"+"0"*64
            plan["candidate"] = cluster.installed(candidate)
            require(cluster.installed(base) == plan["baseline"], "baseline content changed")
            plan["rollback"] = "restore-required" if scenario == "restore-required" else "compatible"
            digest = hashlib.sha256(json.dumps(plan, sort_keys=True).encode()).hexdigest()
            journal = Journal(cluster, digest, None)
            cluster.resources.append(("configmap", journal.name))
            run_phase(cluster, journal, "preflight")
            require(cluster.installed(candidate) == plan["candidate"], "candidate content changed")
            if scenario == "bad-join":
                # The candidate runs and serves the marker over TCP, but a real
                # Minecraft client cannot log in to this HTTP-only listener.
                cluster.reject_join_revision = plan["candidate"]["revision"]
                try:
                    run_phase(cluster, journal, "canary")
                except Halt as exc:
                    require("game client did not join (VERDICT FAIL_" in str(exc), str(exc))
                    require(journal.data.get("active", {}).get("name") == clone
                            and "candidate-canary" not in journal.data["receipts"], "failed at the wrong stage")
                else:
                    raise Halt("running candidate admitted despite a failed wire login")
                require(cluster.read(clone, "wget", "-qO-", "http://127.0.0.1:25565/marker") == "world-a",
                        "candidate was not serving during the failed join")
                try:
                    run_phase(cluster, journal, "promote")
                except Halt as exc:
                    require("complete the rollback rehearsal" in str(exc), str(exc))
                else:
                    raise Halt("fleet admitted after failed wire login")
                # Reset only the disposable clone for the independent bad-image
                # case. This remains outside the halted production rollout.
                cluster.reject_join_revision = None
                require(cluster.installed(base) == plan["baseline"], "manual reset selected another baseline")
                cluster.patch(cluster.server(clone), {"/spec/templateRollout/requestedRevision": rev})
                cluster.baseline_ready(clone, rev)
            elif scenario == "bad-image":
                cluster.timeout = 90
                try:
                    run_phase(cluster, journal, "canary")
                except Halt:
                    require(journal.data.get("active", {}).get("name") == clone
                            and "candidate-canary" not in journal.data["receipts"], "failed at the wrong stage")
                else:
                    raise Halt("unpullable canary passed")
                statuses = cluster.get("pod", clone+"-0")["status"].get("containerStatuses", [])
                require(any(s["name"] == "game" and s.get("state", {}).get("waiting", {}).get("reason")
                            in ("ErrImagePull", "ImagePullBackOff") for s in statuses), "expected actual image pull failure")
            else:
                run_phase(cluster, journal, "canary")
                if scenario == "restore-required":
                    require(cluster.read(clone, "cat", "/data/format2") == "migrated", "fixture did not migrate")
                require(cluster.installed(base) == plan["baseline"], "rollback selected another baseline")
                run_phase(cluster, journal, "rollback")
                require("rollback-canary" in journal.data["receipts"], "rollback receipt missing")
                if scenario == "restore-required":
                    cluster.read(clone, "test", "!", "-e", "/data/format2")
                    cluster.resources.append(("restores.gameplane.local", "gp-rollback-"+digest[:20]))
            require(held_pod == cluster.get("pod", source+"-0")["metadata"]["uid"], "canary rolled a fleet member")
            print(json.dumps({"scenario": scenario, "observations": journal.data}, sort_keys=True), flush=True)
            journal.release()
            command([*cluster.k, "delete", "configmap", journal.name])
    finally:
        for resource, name in reversed(cluster.resources):
            command([*cluster.k, "delete", resource, name, "--ignore-not-found", "--wait=false"])


if __name__ == "__main__":
    run(*sys.argv[1:])
