#!/usr/bin/env python3
"""Append-a-layer experiment: python3 append.py <name> <v1|v2> [--copy-base]

No BuildKit: mise install runs in the runner base image itself (unpacked by
the node's containerd), crane tars what it installed into one layer and
appends it to the base image's manifest in the in-cluster registry, with the
ENV the toolchain Dockerfile sets. --copy-base first copies the base image
from GHCR into the in-cluster registry (once per runner tag)."""
import json, subprocess, sys, time, os

here = os.path.dirname(os.path.abspath(__file__))
name, version = sys.argv[1], sys.argv[2]
copy_base = "--copy-base" in sys.argv
d = json.load(open(os.path.join(here, version + ".json")))
proxy = d["proxy"]
toml = open(os.path.join(here, f"mise-{version}.toml")).read()
REG = "frankie-registry.frankie.svc:5000"
BASE_SRC = "ghcr.io/misarescu/frankie-runner:sha-a717546"
BASE = f"{REG}/base/frankie-runner:sha-a717546"
TARGET = f"{REG}/experiments/goodreads-clone:{name}"
noproxy = "localhost,127.0.0.1,.svc,.cluster.local"
proxy_env = [{"name": n, "value": proxy} for n in ("HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy")] + \
            [{"name": n, "value": noproxy} for n in ("NO_PROXY", "no_proxy")]
data = "/home/agent/.local/share/mise"
install = (
    "set -e; t0=$(date +%s); mkdir -p /home/agent/toolchain /home/agent/.local/share /home/agent/.local/state; "
    "cp /ctx/mise.toml /home/agent/toolchain/; cd /home/agent/toolchain && mise trust && mise install; "
    'echo "STEP install $(( $(date +%s) - t0 ))s"; du -sh ' + data)
base_path = "/home/agent/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
push = "set -e; t0=$(date +%s); "
if copy_base:
    push += f"crane copy --insecure {BASE_SRC} {BASE}; " + 'echo "STEP copy-base $(( $(date +%s) - t0 ))s"; t0=$(date +%s); '
push += (
    "cd / && tar -cf /tmp/layer.tar home/agent/.local/share/mise home/agent/.local/state/mise home/agent/toolchain; "
    'echo "STEP tar $(( $(date +%s) - t0 ))s $(du -h /tmp/layer.tar | cut -f1)"; t0=$(date +%s); '
    f"crane append --insecure -b {BASE} -f /tmp/layer.tar -t {TARGET}; "
    'echo "STEP append $(( $(date +%s) - t0 ))s"; t0=$(date +%s); '
    f"crane mutate --insecure {TARGET} --env MISE_DATA_DIR={data} --env MISE_TRUSTED_CONFIG_PATHS=/workspace "
    f"--env PATH={data}/shims:{base_path} -t {TARGET}; "
    'echo "STEP mutate $(( $(date +%s) - t0 ))s"; '
    f"crane digest --insecure {TARGET}")
labels = {"frankie.dev/role": "toolchain-build", "frankie.dev/experiment": "m5.2"}
cm = {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": name, "labels": labels}, "data": {"mise.toml": toml}}
home = {"name": "home", "mountPath": "/home/agent"}
job = {"apiVersion": "batch/v1", "kind": "Job", "metadata": {"name": name, "labels": labels},
  "spec": {"backoffLimit": 0, "activeDeadlineSeconds": 3000, "ttlSecondsAfterFinished": 3600,
   "template": {"metadata": {"labels": labels}, "spec": {
    "runtimeClassName": "kata", "serviceAccountName": "frankie-build", "automountServiceAccountToken": False,
    "enableServiceLinks": False, "restartPolicy": "Never", "securityContext": {"fsGroup": 10001},
    "initContainers": [{"name": "install", "image": BASE_SRC, "command": ["/bin/sh", "-c", install],
      "securityContext": {"runAsUser": 10001, "runAsGroup": 10001},
      "env": proxy_env + [{"name": "MISE_DATA_DIR", "value": data}, {"name": "HOME", "value": "/home/agent"},
                          {"name": "GOPROXY", "value": "http://frankie-athens.frankie.svc:3000"}],
      "resources": {"limits": {"cpu": "2", "memory": "4Gi"}, "requests": {"cpu": "250m", "memory": "4Gi"}},
      "volumeMounts": [home, {"name": "ctx", "mountPath": "/ctx"}]}],
    "containers": [{"name": "push", "image": "gcr.io/go-containerregistry/crane:debug",
      "command": ["/busybox/sh", "-c", push], "env": proxy_env,
      "resources": {"limits": {"cpu": "2", "memory": "4Gi"}, "requests": {"cpu": "250m", "memory": "4Gi"}},
      "volumeMounts": [home]}],
    "volumes": [{"name": "home", "emptyDir": {"sizeLimit": "20Gi"}},
                {"name": "ctx", "configMap": {"name": name}}]}}}}

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
log = ""
for c in ("install", "push"):
    log += f"=== {c}\n" + subprocess.run(["kubectl", "-n", "frankie-build", "logs", pod, "-c", c], capture_output=True, text=True).stdout
open(os.path.join(here, "logs", name + ".log"), "w").write(log)
steps = [l for l in log.splitlines() if l.startswith("STEP") or l.startswith("sha256:")]
print(f"{name}: append {version} {'ok' if conds.get('Complete') == 'True' else 'FAILED'} wall={wall:.0f}s {steps}")
