import json
import unittest
from unittest.mock import Mock

from wake_burst import ACK, OWNER, REQUEST, SLEEP, Cluster, summary, validate, verdict


def target(name):
    return {"name": name, "namespace": "games", "game": "minecraft-java", "path": "Service",
            "protocol": "tcp", "addr": f"{name}:25565", "clients": 2, "attemptSeconds": 10,
            "retrySeconds": 1, "objectiveSeconds": 30, "deadlineSeconds": 60}


class WakeBurstTests(unittest.TestCase):
    def test_only_positive_join_counts(self):
        for code, line in [(0, "VERDICT\tPASS\tQUERY\tping"), (2, "VERDICT\tPASS\tJOINED\tworld"),
                           (0, "VERDICT\tFAIL_WRONG_DEPTH\tJOINED\tworld"), (0, "VERDICT\tPASS\tJOINED\t ")]:
            self.assertFalse(verdict(code, line)[0])
        self.assertTrue(verdict(0, "VERDICT\tPASS\tJOINED\tWorldData frame")[0])

    def test_partial_and_timeout_are_retained_in_denominator(self):
        results = [{"joined": ok, "elapsedMs": ms, "attempts": [None] * count}
                   for ok, ms, count in [(True, 100, 1), (True, 2000, 2), (False, 3000, 3)]]
        got = summary(results, 1)
        self.assertEqual((got["joined"], got["clients"], got["withinObjective"], got["retries"]), (2, 3, 1, 3))
        self.assertEqual(got["p99JoinedMs"], 2000)
        self.assertIsNone(summary(results[2:], 1)["p95JoinedMs"])

    def test_unsupported_udp_requires_real_adapter(self):
        one, two = target("one"), target("two")
        validate([one, two])
        two.update(game="factorio", protocol="udp")
        with self.assertRaises(ValueError):
            validate([one, two])
        two["adapter"] = "/staging/factorio-join"
        validate([one, two])
        with self.assertRaises(ValueError):
            validate([one, one])

    def test_recovery_rejects_duplicate_boot_restart_owner_and_missed_wake(self):
        cluster = Cluster("test")
        state = {"metadata": {"uid": "gs", "annotations": {REQUEST: "new", ACK: "new"}}, "spec": {}}
        cluster.get = Mock(return_value=state)
        base = {"gameUID": "gs", "statefulSetUID": "sts", "previousWake": "old", "podRV": "123"}
        pod = {"metadata": {"uid": "p1", "ownerReferences": [{"controller": True, "uid": "sts"}]},
               "spec": {"nodeName": "worker"}, "status": {"containerStatuses": [{"name": "game", "restartCount": 0}]}}
        def event():
            return json.dumps({"type": "ADDED", "object": pod})
        cluster.text = Mock(return_value=event())
        self.assertTrue(cluster.recovery(target("one"), base)["passed"])
        for field, value in [(SLEEP, "timestamp"), (ACK, "old"), (OWNER, "backup")]:
            annotations = state["metadata"]["annotations"]
            original = annotations.copy()
            annotations[field] = value
            self.assertFalse(cluster.recovery(target("one"), base)["passed"])
            state["metadata"]["annotations"] = original
        first = event()
        pod["metadata"]["uid"] = "p2"
        cluster.text.return_value = first + "\n" + event()
        self.assertFalse(cluster.recovery(target("one"), base)["passed"])
        pod["status"]["containerStatuses"][0]["restartCount"] = 1
        cluster.text.return_value = event()
        self.assertFalse(cluster.recovery(target("one"), base)["passed"])
        cluster.text.return_value = json.dumps({"type": "ERROR", "object": {"code": 410}})
        with self.assertRaises(ValueError):
            cluster.recovery(target("one"), base)
