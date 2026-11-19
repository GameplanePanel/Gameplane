"""Exercise rollout decisions independently of cluster scheduling and registry access."""
import unittest
from copy import deepcopy
from types import SimpleNamespace
from unittest.mock import patch

from canary import Cluster, Halt, Journal, run_phase, selected_image, validate_module, validate_restore


def metadata(name):
    return {"name": name, "namespace": "games", "uid": name+"-uid", "generation": 1}


class Fleet:
    def __init__(self, mode):
        self.plan = {"source": "source", "canary": {"name": "clone"}, "targets": [{"name": n} for n in ("source", "other")],
                     "module": "game", "backup": "bk", "restore": "restore", "rollback": mode,
                     "baseline": {"revision": "a"*64, "image": "old"}, "candidate": {"revision": "b"*64, "image": "new"}}
        self.names = ["source", "clone", "other"]
        self.running = {n: self.plan["baseline"]["revision"] for n in self.names}
        self.suspend = {n: False for n in self.names}
        self.world = {n: 1 for n in self.names}
        self.uid = {n: n+"-uid" for n in self.names}
        self.lane = "baseline"
        self.fail_join = ""
        self.admissions = []
        self.restores = []
        self.volume_suffix = ""

    def server(self, name):
        return {"metadata": {**metadata(name), "uid": self.uid[name]},
                "spec": {"suspend": self.suspend[name], "templateRef": {"name": "game"},
                         "templateRollout": {"policy": "OnRequest"}},
                "status": {"templateRevision": {"effective": self.running[name], "running": self.running[name],
                                                 "desired": self.plan[self.lane]["revision"]}}}

    def claims(self, gs):
        name = gs["metadata"]["name"]
        return {name+"-"+v: name+"-"+v+"-uid"+self.volume_suffix for v in ("data", "extra", "mods")}

    def inventory(self):
        return dict(self.uid)

    def get(self, kind, name):
        def coverage(server):
            return [{"logicalName": v, "claimName": server+"-"+v, "claimUID": server+"-"+v+"-uid",
                     "included": v != "mods", "classification": "rebuildable" if v == "mods" else "data"}
                    for v in ("data", "extra", "mods")]
        if kind == "backup":
            return {"metadata": metadata(name), "spec": {"serverRef": {"name": "source"}},
                    "status": {"phase": "Succeeded", "snapshotID": "snapshot", "captureManifest": {
                        "formatVersion": 2, "volumePolicy": "all-data", "serverUID": "source-uid",
                        "quiesceOutcome": "already-stopped", "templateRevision": "a"*64, "volumes": coverage("source")}}}
        return {"metadata": metadata(name), "spec": {"serverRef": {"name": "clone"}, "backupRef": {"name": "bk"}},
                "status": {"phase": "Succeeded", "snapshotID": "snapshot", "recoveryPlan": {"backupUID": "bk-uid",
                           "targetUID": "clone-uid", "templateRevision": "a"*64, "manifestDigest": "manifest",
                           "volumes": coverage("clone")}}}

    def pins(self, lane):
        if lane != self.lane:
            raise Halt("wrong installed signed module")
        return {"image": self.plan[lane]["image"]}

    def patch(self, gs, fields):
        name = gs["metadata"]["name"]
        if "/spec/suspend" in fields:
            self.suspend[name] = fields["/spec/suspend"]
        if "/spec/templateRollout/requestedRevision" in fields:
            self.running[name] = fields["/spec/templateRollout/requestedRevision"]
            self.admissions.append((name, self.lane))
            if self.lane == "candidate" and self.plan["rollback"] == "restore-required":
                self.world[name] = 2

    def wait_server(self, name, uid, revision, suspended=False):
        assert self.uid[name] == uid and self.running[name] == revision and self.suspend[name] == suspended
        return self.server(name)

    def probe(self, target, gs, pins):
        name = target["name"]
        if name == self.fail_join:
            raise Halt("registration reached PARTIAL only")
        if pins == self.plan["baseline"] and self.world[name] != 1:
            raise Halt("older engine cannot load format 2")
        return {"serverUID": self.uid[name], "pvcs": self.claims(gs), "join": "JOINED", "save": "ack"}

    def restore(self, name, backup, restore_name):
        assert self.suspend[name]
        self.world[name] = 1
        self.restores.append(name)
        return self.get("restore", restore_name)


class Tests(unittest.TestCase):
    def setup_fleet(self, mode="compatible"):
        fleet = Fleet(mode)
        journal = SimpleNamespace(data={"plan": "1234567890", "receipts": {}}, write=lambda: None)
        run_phase(fleet, journal, "preflight")
        fleet.lane = "candidate"
        return fleet, journal

    def test_bad_canary_never_admits_a_fleet_member(self):
        fleet, journal = self.setup_fleet()
        fleet.fail_join = "clone"
        with self.assertRaises(Halt):
            run_phase(fleet, journal, "canary")
        with self.assertRaises(Halt):
            run_phase(fleet, journal, "promote")
        self.assertEqual(fleet.admissions, [])
        fleet.fail_join = ""
        run_phase(fleet, journal, "canary")
        self.assertEqual(fleet.admissions, [("clone", "candidate")])

    def test_bad_image_after_admission_halts_before_fleet(self):
        fleet, journal = self.setup_fleet()
        original_probe = fleet.probe
        def check(target, gs, pins):
            if pins == fleet.plan["candidate"]:
                raise Halt("candidate image cannot boot")
            return original_probe(target, gs, pins)
        fleet.probe = check
        with self.assertRaisesRegex(Halt, "cannot boot"):
            run_phase(fleet, journal, "canary")
        self.assertEqual(fleet.admissions, [("clone", "candidate")])
        self.assertNotIn("candidate-canary", journal.data["receipts"])

    def test_both_rollback_classes_and_per_target_halt(self):
        for mode in ("compatible", "restore-required"):
            with self.subTest(mode=mode):
                fleet, journal = self.setup_fleet(mode)
                run_phase(fleet, journal, "canary")
                fleet.lane = "baseline"
                run_phase(fleet, journal, "rollback")
                self.assertEqual(fleet.restores, ["clone"] if mode == "restore-required" else [])
                fleet.lane = "candidate"
                fleet.fail_join = "source"
                with self.assertRaises(Halt):
                    run_phase(fleet, journal, "promote")
                self.assertNotIn(("other", "candidate"), fleet.admissions)
                fleet.fail_join = ""
                run_phase(fleet, journal, "promote")
                self.assertTrue(journal.data["complete"])
                self.assertEqual(journal.data["receipts"]["fleet/other"]["from"], "a"*64)

    def test_changed_or_omitted_claims_refuse_admission(self):
        fleet, journal = self.setup_fleet()
        fleet.volume_suffix = "-replaced"
        with self.assertRaisesRegex(Halt, "claims were replaced"):
            run_phase(fleet, journal, "canary")
        backup, restore = fleet.get("backup", "bk"), fleet.get("restore", "restore")
        backup["status"]["captureManifest"]["volumes"][-1]["classification"] = "unclassified"
        with self.assertRaises(Halt):
            validate_restore(backup, restore, fleet.server("source"), fleet.server("clone"))

    def test_signed_digest_and_version_fail_closed(self):
        digest = "sha256:"+"a"*64
        module = {"metadata": metadata("module"), "spec": {"version": "1.0.0", "digest": digest},
                  "status": {"phase": "Ready", "appliedVersion": "1.0.0", "observedGeneration": 1,
                             "verifyPolicy": "keyed", "appliedDigest": digest, "verifiedDigest": digest}}
        validate_module(module, digest)
        for field in ("verifiedDigest", "appliedDigest", "verifyPolicy", "observedGeneration"):
            changed = deepcopy(module)
            del changed["status"][field]
            with self.assertRaises(Halt):
                validate_module(changed, digest)
        with self.assertRaises(Halt):
            selected_image({"versions": [{"id": "a", "image": "x"}]}, {"spec": {"version": "unknown"}})

    def test_held_journal_cannot_be_taken_implicitly(self):
        cm = '{"data":{"progress":"{\\"owner\\":\\"existing\\",\\"plan\\":\\"abc\\"}"}}'
        cluster = SimpleNamespace(k=["kubectl"], plan={"module": "game"})
        with patch("canary.command", return_value=cm):
            with self.assertRaisesRegex(Halt, "held"):
                Journal(cluster, "abc", None)

    def test_udp_joins_only_the_declared_port_on_the_qualified_pod(self):
        cluster = object.__new__(Cluster)
        cluster.plan = {"protocol": "UDP", "port": 8303}
        cluster.client_join = lambda address: address
        for ip, endpoint in (("10.0.3.19", "10.0.3.19:8303"), ("fd00::19", "[fd00::19]:8303")):
            port = {"containerPort": 8303, "protocol": "UDP"}
            pod = {"status": {"podIP": ip},
                   "spec": {"containers": [{"name": "game", "ports": [port]}]}}
            self.assertEqual(cluster.join(pod), endpoint)
            port["protocol"] = "TCP"
            with self.assertRaisesRegex(Halt, "protocol absent"):
                cluster.join(pod)

    def test_save_and_join_are_tied_to_same_owned_instance(self):
        cluster = object.__new__(Cluster)
        cluster.k, cluster.timeout = ["kubectl"], 60
        cluster.plan = {"save": {"argv": ["save"], "expected": "ok"}}
        gs = {"metadata": metadata("clone")}
        target = {"name": "clone", "checks": [{"claim": "clone-data", "argv": ["read"], "expected": "ok"}]}
        cluster.claims = lambda _: {"clone-data": "uid"}
        cluster.join = lambda _: "login success"
        cluster.server = lambda _: gs
        status = {"containerStatuses": [{"name": "game", "imageID": "image", "containerID": "container"}]}
        pods = iter([{"metadata": metadata("first"), "status": status}, {"metadata": metadata("replacement"), "status": status}])
        cluster.pod = lambda *_: next(pods)
        with patch("canary.command", return_value="ok"):
            with self.assertRaisesRegex(Halt, "identity changed"):
                cluster.probe(target, gs, {"revision": "a"*64, "image": "image"})
