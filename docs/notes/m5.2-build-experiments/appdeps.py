#!/usr/bin/env python3
"""Dependency image by appending a layer: python3 appdeps.py <name> <toolchain digest>

The warm-up runs in the toolchain image itself; every file it created or
changed (by ctime, after a marker) is tarred in that container and appended
to the toolchain image's manifest."""
import json, subprocess, sys, time, os, base64

here = os.path.dirname(os.path.abspath(__file__))
name, digest = sys.argv[1], sys.argv[2]
proxy = json.load(open(os.path.join(here, "v1.json")))["proxy"]
REG = "frankie-registry.frankie.svc:5000"
TOOLCHAIN_PULL = f"localhost:30500/experiments/goodreads-clone@{digest}"
TOOLCHAIN = f"{REG}/experiments/goodreads-clone@{digest}"
TARGET = f"{REG}/experiments/goodreads-clone-deps:{name}"
noproxy = "localhost,127.0.0.1,.svc,.cluster.local"
proxy_env = [{"name": n, "value": proxy} for n in ("HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy")] + \
            [{"name": n, "value": noproxy} for n in ("NO_PROXY", "no_proxy")]
warm = (
    "set -e; touch /tmp/marker; sleep 1; t0=$(date +%s); mkdir -p /workspace/deps && tar -xf /ctx/deps.tar -C /workspace/deps; "
    "cd /workspace/deps && mise run deps:warm; "
    'echo "STEP warm $(( $(date +%s) - t0 ))s"; t0=$(date +%s); '
    "find / -xdev -cnewer /tmp/marker ! -path '/tmp*' ! -path '/out*' ! -path '/ctx*' ! -path '/proc*' ! -path '/sys*' ! -path '/dev*' "
    "! -path /etc/hosts ! -path /etc/hostname ! -path /etc/resolv.conf ! -path / > /out/list 2>/dev/null || true; "
    "tar -cf /out/layer.tar --no-recursion -T /out/list 2>/dev/null; "
    'echo "STEP tar $(( $(date +%s) - t0 ))s $(wc -l < /out/list) entries $(du -h /out/layer.tar | cut -f1)"; '
    "du -sh ~/go ~/.cache/go-build ~/.bun 2>/dev/null || true")
push = ("set -e; t0=$(date +%s); "
        f"crane append --insecure -b {TOOLCHAIN} -f /out/layer.tar -t {TARGET}; "
        'echo "STEP append $(( $(date +%s) - t0 ))s"')
labels = {"frankie.dev/role": "toolchain-build", "frankie.dev/experiment": "m5.2"}
cm = {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": name, "labels": labels},
      "binaryData": {"deps.tar": base64.b64encode(open(os.path.join(here, "deps.tar"), "rb").read()).decode()}}
res = {"limits": {"cpu": "2", "memory": "4Gi"}, "requests": {"cpu": "250m", "memory": "4Gi"}}
job = {"apiVersion": "batch/v1", "kind": "Job", "metadata": {"name": name, "labels": labels},
  "spec": {"backoffLimit": 0, "activeDeadlineSeconds": 3000, "ttlSecondsAfterFinished": 3600,
   "template": {"metadata": {"labels": labels}, "spec": {
    "runtimeClassName": "kata", "serviceAccountName": "frankie-build", "automountServiceAccountToken": False,
    "enableServiceLinks": False, "restartPolicy": "Never",
    "initContainers": [{"name": "warm", "image": TOOLCHAIN_PULL, "command": ["/bin/bash", "-c", warm],
      "securityContext": {"runAsUser": 10001, "runAsGroup": 10001},
      "env": proxy_env + [{"name": "GOPROXY", "value": "http://frankie-athens.frankie.svc:3000"}],
      "resources": res, "volumeMounts": [{"name": "out", "mountPath": "/out"}, {"name": "ctx", "mountPath": "/ctx"}]}],
    "containers": [{"name": "push", "image": "gcr.io/go-containerregistry/crane:debug",
      "command": ["/busybox/sh", "-c", push], "env": proxy_env, "resources": res,
      "volumeMounts": [{"name": "out", "mountPath": "/out"}]}],
    "volumes": [{"name": "out", "emptyDir": {"sizeLimit": "20Gi"}}, {"name": "ctx", "configMap": {"name": name}}]}}}}

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
for c in ("warm", "push"):
    log += f"=== {c}\n" + subprocess.run(["kubectl", "-n", "frankie-build", "logs", pod, "-c", c], capture_output=True, text=True).stdout
open(os.path.join(here, "logs", name + ".log"), "w").write(log)
steps = [l for l in log.splitlines() if l.startswith("STEP")]
print(f"{name}: append-deps {'ok' if conds.get('Complete') == 'True' else 'FAILED'} wall={wall:.0f}s {steps}")
