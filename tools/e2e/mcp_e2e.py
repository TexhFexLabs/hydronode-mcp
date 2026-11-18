#!/usr/bin/env python3
"""End-to-end check of a running hnmcp with a real agent token.

Usage: HNMCP_URL=https://<host>/mcp python3 -I mcp_e2e.py <token-file> [--write]

Reads the hoa_ token from a file (never from argv, never printed). Calls every read tool once, following ids from
overview/find/get_sensor, and with --write the safe write tools (mark one message read, dry-run automation).
Prints one line per call: tool, HTTP status, isError, first line of the text.
"""
import json
import os
import sys
import urllib.error
import urllib.request

URL = os.environ.get("HNMCP_URL", "http://localhost:8091/mcp")
TOKEN = open(sys.argv[1]).read().strip()
WRITE = "--write" in sys.argv
_id = 0
failures = []


def rpc(method, params=None, token=TOKEN):
    global _id
    _id += 1
    body = json.dumps({"jsonrpc": "2.0", "id": _id, "method": method, "params": params or {}}).encode()
    req = urllib.request.Request(URL, data=body, method="POST", headers={
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
        "MCP-Protocol-Version": "2025-06-18",
        "Authorization": f"Bearer {token}",
        "User-Agent": "hydronode-mcp-e2e/1",
    })
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.status, dict(r.headers), json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            data = json.loads(raw)
        except Exception:
            data = {}
        return e.code, dict(e.headers), data


def call(name, args=None, expect_error=False):
    status, headers, data = rpc("tools/call", {"name": name, "arguments": args or {}})
    res = data.get("result", {})
    texts = [c.get("text", "") for c in res.get("content", []) if c.get("type") == "text"]
    first = (texts[0] if texts else json.dumps(data.get("error", ""))).splitlines()[0][:110] if (texts or data) else ""
    is_err = res.get("isError", False)
    ok = (is_err == expect_error) and (status in (200, 403) if expect_error else status == 200)
    print(f"{'ok  ' if ok else 'FAIL'} {name:18} {status} err={is_err!s:5} {first}")
    if not ok:
        failures.append(name)
    return res.get("structuredContent") or {}, status, headers


def main():
    st, _, init = rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                      "clientInfo": {"name": "e2e", "version": "1"}})
    instr = init.get("result", {}).get("instructions", "")
    print(f"{'ok  ' if st == 200 and instr else 'FAIL'} initialize         {st} instructions={len(instr)} chars")
    st, _, tl = rpc("tools/list")
    names = [t["name"] for t in tl.get("result", {}).get("tools", [])]
    print(f"{'ok  ' if len(names) == 19 else 'FAIL'} tools/list         {st} {len(names)} tools")
    st, _, pl = rpc("prompts/list")
    print(f"{'ok  ' if len(pl.get('result', {}).get('prompts', [])) == 4 else 'FAIL'} prompts/list       {st}")

    ov, _, _ = call("overview")
    found, _, _ = call("find", {"query": "temp"})
    sensor_id = None
    for s in (found.get("sensors") or []):
        sensor_id = s.get("id")
        break
    point = next(iter(found.get("points") or []), None)
    if not sensor_id and point:
        sensor_id = (point.get("sensor") or {}).get("id")
    if not sensor_id:
        print("FAIL find gave no sensor id")
        failures.append("find-ids")
        return
    sensor, _, _ = call("get_sensor", {"sensor_id": sensor_id})
    numeric = [p for p in sensor.get("points", []) if p.get("kind") == "NUMBER"]
    pid = (point or {}).get("id") or (numeric[0]["id"] if numeric else None)
    if pid:
        summ, _, _ = call("summarize_history", {"point_id": pid, "from": "7d"})
        buckets = summ.get("buckets") or []
        print(f"{'ok  ' if len(buckets) <= 48 else 'FAIL'} buckets<=48        {len(buckets)}")
        avg = summ.get("avg")
        call("find_periods", {"point_id": pid, "below": avg if isinstance(avg, (int, float)) else 0, "from": "2d"})
        call("compare", {"point_ids": [pid], "shift": "1d", "from": "1d"})
    call("list_anomalies", {"state": "all"})
    an, _, _ = call("list_anomalies")
    first_an = next(iter(an.get("items") or []), None)
    if first_an:
        call("get_anomaly", {"anomaly_id": first_an["id"]})
    msgs, _, _ = call("list_messages")
    call("device_health")
    call("console", {"sensor_id": sensor_id, "limit": 5})
    call("public_stations", {"near": "52.52,13.40", "radius_km": 50})
    autos, _, _ = call("list_automations")
    first_auto = next(iter(autos.get("items") or []), None)
    if first_auto:
        call("list_automations", {"automation_id": first_auto["id"]})

    if WRITE:
        first_msg = next(iter(msgs.get("items") or []), None)
        if first_msg:
            call("mark_messages", {"message_ids": [first_msg["id"]], "action": "read"})
        call("create_automation", {"definition": {"name": "e2e dry run", "sensorId": sensor_id,
                                                  "conditions": [{"measurementType": "TEMPERATURE", "comparison": "ABOVE", "threshold": 40}],
                                                  "actions": [{"type": "WEBHOOK", "url": "https://example.com/x"}]}},
             expect_error=True)
    else:
        _, status, headers = call("mark_messages", {"all": True, "action": "read"}, expect_error=True)
        www = next((v for k, v in headers.items() if k.lower() == "www-authenticate"), "")
        ok = status == 403 and "insufficient_scope" in www
        print(f"{'ok  ' if ok else 'FAIL'} step-up 403        {status} {www[:90]}")
        if not ok:
            failures.append("step-up")

    for bad in ("hat_x", "eyJx.y.z"):
        st, h, _ = rpc("tools/list", token=bad)
        print(f"{'ok  ' if st == 401 else 'FAIL'} wrong token {bad[:4]}   {st}")
        if st != 401:
            failures.append("wrong-token")


main()
print("ALL OK" if not failures else f"FAILED: {', '.join(failures)}")
sys.exit(1 if failures else 0)
