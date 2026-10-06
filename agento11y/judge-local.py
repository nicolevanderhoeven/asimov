#!/usr/bin/env python3
"""Run this directory's evaluators locally, with your Anthropic key.

The online rules judge generations in your Grafana Cloud stack, on the
judge model the stack provides. When that isn't available (the stack's LLM
budget ran out, say), this script judges the same generations with the same
evaluator prompts and the same judge model, and reports pass rates per
evaluator. It reads the rules and evaluators in agento11y/, conversations
through gcx, and the evaluators with yq.

    agento11y/judge-local.py --experiment exp-...            # judge a run
    agento11y/judge-local.py --calibrate exp-...             # compare with
                                                             # online scores

Rules sample whole conversations at their sample_rate, as online; --sample
overrides every rule's rate. Each verdict goes to --out (JSON lines), and
--export also sends it to Agent Observability as a score whose evaluator_id
is local.<evaluator id>, so it never mixes with the online evaluators'.

It approximates the server's rendering of the {{...}} variables: the GM's
system prompt, the latest user message (with any notes the game added), the
history before it, the generation's own tool calls, and every tool result in
its input. Run --calibrate on generations the stack already scored to see
how closely it agrees.
"""
import argparse
import base64
import concurrent.futures
import hashlib
import json
import os
import pathlib
import subprocess
import sys
import threading
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent
ANTHROPIC_URL = "https://api.anthropic.com/v1/messages"


def load_env():
    env = ROOT.parent / ".env"
    if not env.exists():
        return
    for line in env.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        os.environ.setdefault(k.strip(), v.strip().strip('"').strip("'"))


def yaml(path):
    return json.loads(subprocess.run(["yq", "-o", "json", ".", str(path)], check=True, capture_output=True, text=True).stdout)


def gcx(*args):
    out = subprocess.run(["gcx", "agento11y", *args, "-o", "json"], capture_output=True, text=True, env={**os.environ, "GCX_AGENT_MODE": "true"})
    if out.returncode != 0:
        raise RuntimeError(f"gcx {' '.join(args)}: {out.stderr.strip() or out.stdout.strip()}")
    return json.loads(out.stdout)


def b64json(s):
    try:
        return json.loads(base64.b64decode(s))
    except Exception:
        return s


def text_of(msg):
    return "\n".join(p["text"] for p in msg.get("parts", []) if p.get("text"))


def call_results(conv):
    """Every tool result in a conversation, by tool call ID: a generation
    that makes a call sees its result only in the next one's input."""
    out = {}
    for g in conv["generations"]:
        for m in g.get("input") or []:
            for p in m.get("parts", []):
                if "tool_result" in p:
                    r = p["tool_result"]
                    out[r.get("tool_call_id")] = {"name": r.get("name"), "tool_call_id": r.get("tool_call_id"), "content": b64json(r.get("content_json", ""))}
    return out


def render(gen, results_by_call=None):
    """The {{...}} variables for one generation."""
    msgs = gen.get("input") or []
    users = [i for i, m in enumerate(msgs) if m.get("role") == "MESSAGE_ROLE_USER" and text_of(m)]
    last = users[-1] if users else None
    history = []
    for m in msgs[: last if last is not None else 0][-12:]:
        t = text_of(m)
        if t:
            history.append(f"{m['role'].removeprefix('MESSAGE_ROLE_').lower()}: {t}")
    results = []
    for m in msgs:
        for p in m.get("parts", []):
            if "tool_result" in p:
                r = p["tool_result"]
                results.append({"name": r.get("name"), "tool_call_id": r.get("tool_call_id"), "content": b64json(r.get("content_json", ""))})
    calls, texts = [], []
    for m in gen.get("output") or []:
        for p in m.get("parts", []):
            if "tool_call" in p:
                c = p["tool_call"]
                calls.append({"id": c.get("id"), "name": c.get("name"), "input": b64json(c.get("input_json", ""))})
            elif p.get("text"):
                texts.append(p["text"])
    have = {r["tool_call_id"] for r in results}
    for c in calls:
        r = (results_by_call or {}).get(c["id"])
        if r and c["id"] not in have:
            results.append(r)
    return {
        "system_prompt": gen.get("system_prompt") or "",
        "latest_user_message": text_of(msgs[last]) if last is not None else "",
        "user_history": "\n\n".join(history),
        "tool_calls": json.dumps(calls, indent=1) if calls else "(none)",
        "tool_results": json.dumps(results, indent=1) if results else "(none)",
        "assistant_response": "\n\n".join(texts),
    }


def fill(template, vars):
    for k, v in vars.items():
        template = template.replace("{{" + k + "}}", v)
    return template


def judge(ev, gen, model, results_by_call):
    key = ev["output_keys"][0]["key"]
    tool = {
        "name": "verdict",
        "description": "Report the verdict.",
        "input_schema": {
            "type": "object",
            "properties": {key: {"type": "boolean", "description": ev["output_keys"][0].get("description", "")}, "explanation": {"type": "string"}},
            "required": [key, "explanation"],
        },
    }
    body = {
        "model": model,
        "max_tokens": max(int(ev["config"].get("max_tokens", 300)), 400),
        "temperature": ev["config"].get("temperature", 0),
        "system": ev["config"]["system_prompt"],
        "messages": [{"role": "user", "content": fill(ev["config"]["user_prompt"], render(gen, results_by_call))}],
        "tools": [tool],
        "tool_choice": {"type": "tool", "name": "verdict"},
    }
    req = urllib.request.Request(ANTHROPIC_URL, data=json.dumps(body).encode(), headers={
        "x-api-key": os.environ["ANTHROPIC_API_KEY"], "anthropic-version": "2023-06-01", "content-type": "application/json"})
    for attempt in range(4):
        try:
            with urllib.request.urlopen(req, timeout=120) as res:
                out = json.loads(res.read())
            break
        except urllib.error.HTTPError as e:
            if e.code in (429, 500, 529) and attempt < 3:
                threading.Event().wait(5 * (attempt + 1))
                continue
            raise
    use = next(c for c in out["content"] if c["type"] == "tool_use")["input"]
    value = bool(use.get(key))
    return {"key": key, "value": value, "passed": value == ev["output_keys"][0].get("pass_value", True), "explanation": use.get("explanation", ""), "usage": out.get("usage", {})}


def sampled(conv, rule, rate):
    h = int(hashlib.sha256(f"{rule}/{conv}".encode()).hexdigest()[:8], 16) / 0xFFFFFFFF
    return h < rate


def matches(gen, match):
    for field, allowed in (match or {}).items():
        value = gen.get(field) if not field.startswith("tags.") else (gen.get("tags") or {}).get(field[5:])
        if value not in allowed:
            return False
    return True


def export(items):
    api = (os.environ.get("AGENTO11Y_API_ENDPOINT") or os.environ.get("AGENTO11Y_ENDPOINT") or os.environ.get("GRAFANA_CLOUD_SIGIL_ENDPOINT", ""))
    api = "/".join(api.split("/")[:3])
    tenant = os.environ.get("GRAFANA_CLOUD_INSTANCE_ID") or os.environ.get("GRAFANA_CLOUD_INSTANCE", "")
    auth = base64.b64encode(f"{tenant}:{os.environ['GRAFANA_CLOUD_API_KEY']}".encode()).decode()
    for i in range(0, len(items), 100):
        req = urllib.request.Request(api + "/api/v1/scores:export", data=json.dumps({"scores": items[i:i + 100]}).encode(), headers={
            "Authorization": "Basic " + auth, "X-Scope-OrgID": tenant, "Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=60) as res:
            rejected = [r for r in json.loads(res.read()).get("results", []) if not r.get("accepted") and r.get("status") != "duplicate"]
            if rejected:
                print(f"export: {len(rejected)} rejected: {json.dumps(rejected)[:400]}", file=sys.stderr)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--experiment", help="judge every conversation of this experiment run")
    ap.add_argument("--calibrate", help="judge generations of this experiment that the stack already scored, and compare")
    ap.add_argument("--sample", type=float, help="override every rule's sample rate")
    ap.add_argument("--limit", type=int, default=0, help="judge at most this many generations per evaluator")
    ap.add_argument("--model", default=None, help="judge model (default: each evaluator's own)")
    ap.add_argument("--workers", type=int, default=6)
    ap.add_argument("--out", default="judge-local.jsonl")
    ap.add_argument("--export", action="store_true", help="send verdicts to Agent Observability as local.<evaluator> scores")
    args = ap.parse_args()
    load_env()
    exp = args.experiment or args.calibrate
    if not exp:
        ap.error("give --experiment or --calibrate")
    evaluators = {e["evaluator_id"]: e for e in (yaml(p) for p in sorted((ROOT / "evaluators").glob("*.yaml")))}
    rules = [yaml(p) for p in sorted((ROOT / "rules").glob("*.yaml"))]
    report = gcx("experiments", "get-report", exp)
    convs = [t["trial"]["conversation_id"] for row in report["rows"] for t in row["trials"] if t["trial"].get("conversation_id")]
    print(f"{exp}: {len(convs)} conversations", file=sys.stderr)

    jobs = []  # (evaluator, generation, conversation, online verdict or None)
    results_by_call = {}
    for conv in convs:
        c = gcx("conversations", "get", conv)
        results_by_call.update(call_results(c))
        for rule in rules:
            if not rule.get("enabled", True):
                continue
            rate = args.sample if args.sample is not None else rule.get("sample_rate", 1)
            if not args.calibrate and not sampled(conv, rule["rule_id"], rate):
                continue
            for gen in c["generations"]:
                if not matches(gen, rule.get("match")):
                    continue
                for ev_id in rule["evaluator_ids"]:
                    online = None
                    if args.calibrate:
                        # latest_scores is keyed by score key.
                        key = evaluators[ev_id]["output_keys"][0]["key"]
                        online = (gen.get("latest_scores") or {}).get(key) or {}
                        if online.get("evaluator_id") != ev_id:
                            continue
                    jobs.append((evaluators[ev_id], gen, conv, online))
    if args.limit:
        seen, kept = {}, []
        for j in jobs:
            n = seen.get(j[0]["evaluator_id"], 0)
            if n < args.limit:
                kept.append(j)
                seen[j[0]["evaluator_id"]] = n + 1
        jobs = kept
    print(f"{len(jobs)} judge calls", file=sys.stderr)

    lock = threading.Lock()
    results = []
    with open(args.out, "w") as out, concurrent.futures.ThreadPoolExecutor(args.workers) as pool:
        def run(job):
            ev, gen, conv, online = job
            try:
                v = judge(ev, gen, args.model or ev["config"]["model"], results_by_call)
            except Exception as e:  # recorded, not fatal
                v = {"error": str(e)}
            row = {"evaluator_id": ev["evaluator_id"], "generation_id": gen["generation_id"], "conversation_id": conv, "agent_version": gen.get("agent_version"), **v}
            if online is not None:
                row["online_passed"] = online.get("passed")
            with lock:
                results.append(row)
                out.write(json.dumps(row) + "\n")
                if len(results) % 100 == 0:
                    print(f"  {len(results)}/{len(jobs)}", file=sys.stderr)
        list(pool.map(run, jobs))

    by = {}
    for r in results:
        by.setdefault(r["evaluator_id"], []).append(r)
    total_pass = total = 0
    tokens = sum(r.get("usage", {}).get("input_tokens", 0) for r in results), sum(r.get("usage", {}).get("output_tokens", 0) for r in results)
    print(f"\n{'evaluator':32} {'pass':>9} {'rate':>7}" + ("  agree w/ online" if args.calibrate else ""))
    for ev_id in sorted(by):
        rows = [r for r in by[ev_id] if "error" not in r]
        p = sum(r["passed"] for r in rows)
        total_pass, total = total_pass + p, total + len(rows)
        line = f"{ev_id:32} {p:>4}/{len(rows):<4} {p / max(len(rows), 1):7.1%}"
        if args.calibrate:
            both = [r for r in rows if r.get("online_passed") is not None]
            agree = sum(r["passed"] == r["online_passed"] for r in both)
            line += f"  {agree}/{len(both)} ({agree / max(len(both), 1):.0%}); online pass {sum(bool(r['online_passed']) for r in both)}/{len(both)}"
        print(line)
    errors = sum("error" in r for r in results)
    print(f"{'all':32} {total_pass:>4}/{total:<4} {total_pass / max(total, 1):7.1%}   ({errors} errors; {tokens[0]} input, {tokens[1]} output tokens)")

    if args.export and not args.calibrate:
        items = [{
            "score_id": "score-" + hashlib.sha1(f"local/{r['evaluator_id']}/{r['generation_id']}".encode()).hexdigest()[:16],
            "evaluator_id": "local." + r["evaluator_id"], "evaluator_version": evaluators[r["evaluator_id"]].get("version", "1"),
            "score_key": r["key"], "value": {"bool": r["value"]}, "passed": r["passed"], "explanation": r["explanation"][:2000],
            "generation_id": r["generation_id"], "conversation_id": r["conversation_id"],
            "metadata": {"judge": "local", "experiment_id": exp},
            "source": {"kind": "local_judge", "id": "agento11y/judge-local.py"},
        } for r in results if "error" not in r]
        export(items)
        print(f"exported {len(items)} scores", file=sys.stderr)


if __name__ == "__main__":
    main()
