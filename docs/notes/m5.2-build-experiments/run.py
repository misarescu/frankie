#!/usr/bin/env python3
"""Run one M5.2 build experiment: python3 run.py <name> <v1|v2> <variant> [extra build args...]

Variants change only the build Job Frankie generates (images.BuildResources):
  baseline   as today: BuildKit state in an emptyDir on the Kata VM's virtiofs
  tmpfs      BuildKit state in a memory emptyDir (guest tmpfs), memory limit 8Gi
  pvc        BuildKit state on the PVC bx-state (kept between builds)
  cache      baseline plus --export-cache/--import-cache to the in-cluster registry
Prints the Job's wall time and writes the build log to logs/<name>.log."""
import json, subprocess, sys, time, copy, os

here = os.path.dirname(os.path.abspath(__file__))
name, version, variant = sys.argv[1], sys.argv[2], sys.argv[3]
extra = sys.argv[4:]
d = json.load(open(os.path.join(here, version + ".json")))
cm, job = d["configMap"], d["job"]
for o in (cm, job):
    o["metadata"]["name"] = name
    o["metadata"].pop("creationTimestamp", None)
    o["metadata"]["labels"]["frankie.dev/experiment"] = "m5.2"
job["apiVersion"], job["kind"] = "batch/v1", "Job"
cm["apiVersion"], cm["kind"] = "v1", "ConfigMap"
job.pop("status", None)
spec = job["spec"]["template"]["spec"]
spec["volumes"][0]["configMap"]["name"] = name
c = spec["containers"][0]
# Name the snapshotter BuildKit picked (its state dir is runc-<snapshotter>).
c["command"][2] = c["command"][2] + "; rc=$?; echo BUILDKIT-STATE: $(ls /var/lib/buildkit); df -h /var/lib/buildkit | tail -1; exit $rc"
reg = "frankie-registry.frankie.svc:5000/experiments/cache:goodreads"
if variant == "tmpfs":
    spec["volumes"][1]["emptyDir"] = {"medium": "Memory", "sizeLimit": "6Gi"}
    for k in ("limits", "requests"):
        c["resources"][k]["memory"] = "8Gi"
elif variant == "pvc":
    spec["volumes"][1] = {"name": "scratch", "persistentVolumeClaim": {"claimName": "bx-state"}}
elif variant == "cache":
    c["args"] += ["--export-cache", f"type=registry,ref={reg},mode=max,registry.insecure=true",
                  "--import-cache", f"type=registry,ref={reg},registry.insecure=true"]
elif variant != "baseline":
    sys.exit("unknown variant " + variant)
c["args"] += extra

def kubectl(*args, inp=None):
    return subprocess.run(["kubectl", "-n", "frankie-build", *args], input=inp, capture_output=True, text=True, check=True).stdout

kubectl("delete", "job", name, "--ignore-not-found", "--wait=true")
kubectl("delete", "configmap", name, "--ignore-not-found")
kubectl("apply", "-f", "-", inp=json.dumps({"apiVersion": "v1", "kind": "List", "items": [cm, job]}))
start = time.time()
while True:
    st = json.loads(kubectl("get", "job", name, "-o", "json"))["status"]
    conds = {x["type"]: x["status"] for x in st.get("conditions", [])}
    if conds.get("Complete") == "True" or conds.get("Failed") == "True":
        break
    time.sleep(5)
wall = time.time() - start
pod = kubectl("get", "pods", "-l", "batch.kubernetes.io/job-name=" + name, "-o", "jsonpath={.items[0].metadata.name}")
log = subprocess.run(["kubectl", "-n", "frankie-build", "logs", pod], capture_output=True, text=True).stdout
open(os.path.join(here, "logs", name + ".log"), "w").write(log)
ok = conds.get("Complete") == "True"
state = [l for l in log.splitlines() if l.startswith("BUILDKIT-STATE") or "/var/lib/buildkit" in l]
print(f"{name}: {variant} {version} {'ok' if ok else 'FAILED'} wall={wall:.0f}s {state}")
