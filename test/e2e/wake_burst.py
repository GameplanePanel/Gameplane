#!/usr/bin/env python3
"""Qualify simultaneous joins to existing sleeping GameServers in a staging cluster."""

import argparse
import concurrent.futures
import datetime
import json
import math
import re
import secrets
import shutil
import subprocess
import sys
import threading
import time
from urllib.parse import urlencode


SLEEP = "gameplane.local/idle-asleep-since"
REQUEST = "gameplane.local/idle-wake-requested"
ACK = "gameplane.local/idle-wake-completed"
OWNER = "operations.gameplane.local/owner"
NODE = "gameplane.local/hostport-node"
NODE_UID = "gameplane.local/hostport-node-uid"
DNS_LABEL = re.compile(r"[a-z0-9](?:[-a-z0-9]*[a-z0-9])?")


def validate(targets):
    if not isinstance(targets, list) or not 2 <= len(targets) <= 64:
        raise ValueError("supply 2–64 distinct sleepers")
    servers, endpoints = set(), set()
    for t in targets:
        fields = {"namespace", "name", "game", "addr", "protocol", "path", "adapter",
                  "clients", "objectiveSeconds", "deadlineSeconds", "attemptSeconds", "retrySeconds"}
        if set(t) - fields:
            raise ValueError("unrecognized target fields")
        for key in ("namespace", "name", "game"):
            if not DNS_LABEL.fullmatch(t[key]) or len(t[key]) > 63:
                raise ValueError(f"invalid {key}")
        key = (t["namespace"], t["name"])
        if key in servers or t["addr"] in endpoints or not t["addr"]:
            raise ValueError("each sleeper needs a distinct server and endpoint")
        servers.add(key)
        endpoints.add(t["addr"])
        if t["path"] not in ("Service", "Hostport") or t["protocol"] not in ("tcp", "udp"):
            raise ValueError("invalid endpoint path or protocol")
        if not t.get("adapter") and (t["protocol"] != "tcp" or t["game"] not in ("minecraft-java", "terraria")):
            raise ValueError("this game/protocol requires an entitled join client adapter")
        for field in ("clients", "objectiveSeconds", "deadlineSeconds", "attemptSeconds", "retrySeconds"):
            if type(t[field]) is not int or not 1 <= t[field] <= 1800:
                raise ValueError(f"invalid {field} for {key}")
        if max(t["objectiveSeconds"], t["attemptSeconds"]) > t["deadlineSeconds"]:
            raise ValueError("objective and attempt limit must fit the trial deadline")
    if sum(t["clients"] for t in targets) > 64:
        raise ValueError("at most 64 simultaneous clients")


def verdict(exit_code, output):
    records = [line for line in output.splitlines() if line.startswith("VERDICT\t")]
    if len(records) != 1:
        return False, "no single verdict"
    parts = records[0].split("\t", 3)
    if len(parts) != 4 or not parts[3].strip():
        return False, "missing evidence"
    if exit_code == 0 and parts[1:3] == ["PASS", "JOINED"]:
        return True, parts[3][:512]
    return False, " ".join(parts[1:3])[:80]


def measure_client(t, user, client, start, release):
    release.wait()
    began = start[0]
    deadline = began + t["deadlineSeconds"]
    attempts = []
    while time.monotonic() < deadline:
        remaining = deadline - time.monotonic()
        timeout = min(t["attemptSeconds"], remaining)
        if timeout <= 0:
            break
        before = round((time.monotonic() - began) * 1000)
        # An adapter is a trusted staging executable. There is no shell. Its
        # credentials stay in its local environment, outside the plan/report.
        argv = [t.get("adapter") or client, "-game", t["game"], "-addr", t["addr"],
                "-user", user, "-deadline", f"{max(1, int(timeout * 1000))}ms"]
        try:
            proc = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                  text=True, timeout=timeout, check=False)
            joined, detail = verdict(proc.returncode, proc.stdout)
        except subprocess.TimeoutExpired:
            joined, detail = False, "attempt timed out"
        elapsed = round((time.monotonic() - began) * 1000)
        attempts.append({"startMs": before, "endMs": elapsed, "evidence": detail})
        if joined:
            return {"joined": True, "elapsedMs": elapsed, "attempts": attempts}
        time.sleep(max(0, min(t["retrySeconds"], deadline - time.monotonic())))
    return {"joined": False, "elapsedMs": round((time.monotonic() - began) * 1000), "attempts": attempts}


def summary(results, objective):
    elapsed = sorted(r["elapsedMs"] for r in results if r["joined"])
    def percentile(p):
        return elapsed[math.ceil(p * len(elapsed)) - 1] if elapsed else None
    return {"clients": len(results), "joined": len(elapsed),
            "withinObjective": sum(ms <= objective * 1000 for ms in elapsed),
            "retries": sum(max(0, len(r["attempts"]) - 1) for r in results),
            "p95JoinedMs": percentile(.95), "p99JoinedMs": percentile(.99)}


class Cluster:
    def __init__(self, context):
        self.prefix = ["kubectl", "--context", context, "--request-timeout=15s"]

    def text(self, *args, timeout=20):
        return subprocess.run([*self.prefix, *args], check=True, capture_output=True,
                              text=True, timeout=timeout).stdout

    def get(self, t, kind, name):
        return json.loads(self.text("get", kind, name, "-n", t["namespace"], "-o", "json"))

    def baseline(self, t):
        gs = self.get(t, "gameserver", t["name"])
        annotations = gs["metadata"].get("annotations", {})
        spec = gs["spec"]
        if (not annotations.get(SLEEP) or spec.get("suspend") or annotations.get(OWNER)
                or annotations.get(REQUEST) != annotations.get(ACK)
                or not spec.get("idle", {}).get("wakeOnConnect")):
            raise ValueError(f"{t['name']} is not an unowned, armed sleeper")
        hostport = spec.get("networking", {}).get("expose") == "Hostport"
        if hostport != (t["path"] == "Hostport"):
            raise ValueError(f"{t['name']} endpoint path disagrees with spec")
        node = None
        if hostport:
            node = {"name": annotations[NODE], "uid": annotations[NODE_UID]}
            if self.get(t, "node", node["name"])["metadata"]["uid"] != node["uid"]:
                raise ValueError("Hostport node identity changed before trial")
        sts = self.get(t, "statefulset", t["name"])
        waker = self.get(t, "deployment", t["name"] + "-waker")
        for child in (sts, waker):
            if not any(ref.get("controller") and ref["uid"] == gs["metadata"]["uid"]
                       for ref in child["metadata"].get("ownerReferences", [])):
                raise ValueError(f"{t['name']} workload belongs to a different GameServer")
        pods = json.loads(self.text("get", "pods", "-n", t["namespace"], "--field-selector",
                                    "metadata.name=" + t["name"] + "-0", "-o", "json"))
        if (sts["spec"].get("replicas") != 0 or pods["items"] or
                waker.get("status", {}).get("readyReplicas", 0) != 1):
            raise ValueError(f"{t['name']} requires zero game Pods and one ready sentinel")
        return {"gameUID": gs["metadata"]["uid"], "statefulSetUID": sts["metadata"]["uid"],
                "previousWake": annotations.get(REQUEST), "podRV": pods["metadata"]["resourceVersion"],
                "hostportNode": node}

    def observation(self, target, base):
        gs = self.get(target, "gameserver", target["name"])
        token = gs["metadata"].get("annotations", {}).get(REQUEST)
        return {"wakeRequested": bool(token and token != base["previousWake"]),
                "phase": gs.get("status", {}).get("phase"),
                "asleep": bool(gs["metadata"].get("annotations", {}).get(SLEEP))}

    def recovery(self, target, base):
        gs = self.get(target, "gameserver", target["name"])
        ann = gs["metadata"].get("annotations", {})
        # Replay the API watch from the pre-burst list. Polling the final Pod
        # alone would hide a short-lived duplicate. Compaction invalidates proof.
        query = urlencode({"watch": "true", "timeoutSeconds": 1, "resourceVersion": base["podRV"],
                           "fieldSelector": "metadata.name=" + target["name"] + "-0"})
        raw = self.text("get", "--raw", f"/api/v1/namespaces/{target['namespace']}/pods?{query}")
        pods = {}
        for line in raw.splitlines():
            event = json.loads(line)
            typ, pod = event["type"], event["object"]
            if typ == "BOOKMARK":
                continue
            if typ not in ("ADDED", "MODIFIED", "DELETED"):
                raise ValueError("incomplete Kubernetes Pod watch")
            if not any(ref["uid"] == base["statefulSetUID"] and ref.get("controller")
                       for ref in pod["metadata"].get("ownerReferences", [])):
                raise ValueError("game Pod owner changed")
            uid = pod["metadata"]["uid"]
            restarts = max((c["restartCount"] for c in pod.get("status", {}).get("containerStatuses", [])
                            if c["name"] == "game"), default=0)
            pods[uid] = {"node": pod["spec"].get("nodeName"), "restarts": max(restarts, pods.get(uid, {}).get("restarts", 0)),
                         "deleted": typ == "DELETED" or bool(pod["metadata"].get("deletionTimestamp"))}
        good = (gs["metadata"]["uid"] == base["gameUID"] and not gs["spec"].get("suspend")
                and not ann.get(OWNER) and not ann.get(SLEEP) and ann.get(REQUEST)
                and ann[REQUEST] != base["previousWake"] and ann[REQUEST] == ann.get(ACK)
                and len(pods) == 1 and all(not p["restarts"] and not p["deleted"] for p in pods.values()))
        if node := base.get("hostportNode"):
            good = (good and ann.get(NODE) == node["name"] and ann.get(NODE_UID) == node["uid"]
                    and self.get(target, "node", node["name"])["metadata"]["uid"] == node["uid"]
                    and all(p["node"] == node["name"] for p in pods.values()))
        return {"acknowledged": ann.get(ACK), "requested": ann.get(REQUEST), "owner": bool(ann.get(OWNER)),
                "phase": gs.get("status", {}).get("phase"), "pods": pods, "passed": bool(good)}

    def sample(self):
        at = datetime.datetime.now(datetime.timezone.utc).isoformat()
        sample = {"at": at}
        try:
            nodes = json.loads(self.text("get", "--raw", "/apis/metrics.k8s.io/v1beta1/nodes"))
            sample["usage"] = [{"name": n["metadata"]["name"], "at": n["timestamp"],
                                "window": n["window"], "usage": n["usage"]} for n in nodes["items"]]
        except (subprocess.SubprocessError, ValueError, KeyError):
            sample["usageUnavailable"] = True
        try:
            nodes = json.loads(self.text("get", "nodes", "-o", "json"))
            sample["pressure"] = {n["metadata"]["name"]: {c["type"]: c["status"] for c in
                                  n["status"]["conditions"] if c["type"].endswith("Pressure")}
                                  for n in nodes["items"]}
        except (subprocess.SubprocessError, ValueError, KeyError):
            sample["pressureUnavailable"] = True
        return sample


def run(args, targets):
    validate(targets)
    for exe in {t.get("adapter") or args.client for t in targets}:
        if not shutil.which(exe):
            raise ValueError(f"missing executable: {exe}")
    cluster = Cluster(args.context)
    bases = [cluster.baseline(t) for t in targets]
    restart = args.restart_operator.split("/") if args.restart_operator else None
    if restart and (len(restart) != 2 or not all(DNS_LABEL.fullmatch(s) for s in restart)):
        raise ValueError("operator must be namespace/deployment")
    report = {"context": args.context, "started": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "operatorRestart": "pending" if restart else "not requested",
              "nodeMetrics": [cluster.sample()], "targets": [], "errors": []}
    history = [[] for t in targets]
    release, start = threading.Event(), []
    clients = sum(t["clients"] for t in targets)
    prefix = "gp" + secrets.token_hex(4)
    with concurrent.futures.ThreadPoolExecutor(max_workers=clients) as pool:
        groups = [[pool.submit(measure_client, t, f"{prefix}{i:02x}{j:02x}", args.client, start, release)
                   for j in range(t["clients"])] for i, t in enumerate(targets)]
        futures = [f for group in groups for f in group]
        start.append(time.monotonic())
        release.set()
        while not all(f.done() for f in futures):
            waking = False
            for t, b, timeline in zip(targets, bases, history):
                try:
                    state = cluster.observation(t, b)
                    if not timeline or timeline[-1]["state"] != state:
                        timeline.append({"observedMs": round((time.monotonic() - start[0]) * 1000), "state": state})
                    waking |= state["wakeRequested"]
                except (ValueError, subprocess.SubprocessError) as err:
                    report["errors"].append(f"{t['name']} observation: {err}")
            if restart and report["operatorRestart"] == "pending" and waking and not all(f.done() for f in futures):
                try:
                    cluster.text("rollout", "restart", "deployment/" + restart[1], "-n", restart[0])
                    report["operatorRestart"] = {"deployment": args.restart_operator,
                                                 "requestedMs": round((time.monotonic() - start[0]) * 1000)}
                except subprocess.SubprocessError as err:
                    report["operatorRestart"] = "failed"
                    report["errors"].append(f"operator restart: {err}")
            concurrent.futures.wait(futures, timeout=2)
            report["nodeMetrics"].append(cluster.sample())
        if restart and isinstance(report["operatorRestart"], dict):
            try:
                cluster.text("--request-timeout=125s", "rollout", "status", "deployment/" + restart[1],
                             "-n", restart[0], "--timeout=120s", timeout=130)
            except subprocess.SubprocessError as err:
                report["errors"].append(f"operator recovery: {err}")
        cohorts = {}
        for target, base, group, timeline in zip(targets, bases, groups, history):
            results = [f.result() for f in group]
            key = (target["game"], target["path"], target["protocol"], target["objectiveSeconds"])
            cohorts.setdefault(key, []).extend(results)
            try:
                recovery = cluster.recovery(target, base)
            except (subprocess.SubprocessError, ValueError, KeyError) as err:
                recovery = {"passed": False, "error": str(err)}
            report["targets"].append({"target": {k: v for k, v in target.items() if k != "adapter"},
                                      "baseline": base, "summary": summary(results, target["objectiveSeconds"]),
                                      "clients": results, "observations": timeline, "recovery": recovery})
        report["cohorts"] = [{"game": g, "path": path, "protocol": proto, "objectiveSeconds": limit,
                              **summary(results, limit)} for (g, path, proto, limit), results in cohorts.items()]
    report["joinAndRecoveryPassed"] = (not report["errors"] and all(t["summary"]["withinObjective"] == t["summary"]["clients"] and
                            t["recovery"]["passed"] for t in report["targets"])
                        and (not restart or isinstance(report["operatorRestart"], dict)))
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--plan", required=True)
    parser.add_argument("--client", default="join-once")
    parser.add_argument("--restart-operator", metavar="NAMESPACE/DEPLOYMENT",
                        help="restart this operator after a wake arrives while clients are still joining")
    args = parser.parse_args()
    with open(args.plan, encoding="utf-8") as plan:
        report = run(args, json.load(plan))
    print(json.dumps(report, indent=2))
    return 0 if report["joinAndRecoveryPassed"] else 1


if __name__ == "__main__":
    sys.exit(main())
