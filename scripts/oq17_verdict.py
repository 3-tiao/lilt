import json, sys

payload = json.load(open(sys.argv[1]))
if payload.get("ok"):
    queue = payload["data"].get("queue") or []
    print("ok" if queue else "empty")
    raise SystemExit
error = payload.get("error") or {}
details = error.get("details") or {}
if error.get("code") == "partial_failure" and details.get("queueReady"):
    print("queueReady:%d" % len((details.get("state") or {}).get("queue") or []))
    raise SystemExit
print("error:%s" % error.get("code"))
