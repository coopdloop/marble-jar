#!/usr/bin/env python3
"""Seed Marble-Jar with a believable mock project: objectives, ~100 marbles,
then exercise the update paths (PATCH objectives/marbles + rollup backfill).

Stdlib only. Point it at a running core API:

    export MARBLEJAR_API_KEY=mj_xxx
    python3 scripts/seed_mock_project.py --base-url http://localhost:8080 --count 100

Scopes used (an `mj_` key needs all of them for the full run):
    marbles:write  marbles:read  objectives:write  rollups:write  jar:read
Missing scopes degrade gracefully: the affected step is skipped and reported.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import random
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid
from datetime import datetime, timedelta, timezone

# --------------------------------------------------------------------------
# Mock product: "Lumen", a mid-size analytics SaaS. Six objectives, each with
# its own project, agents and task pool so the jar reads like real work.
# --------------------------------------------------------------------------

OBJECTIVES = [
    {
        "title": "Ship metered billing v2",
        "description": "Replace seat-based invoicing with usage meters, proration and dunning.",
        "project": "lumen-api",
        "budget_tokens": 4_000_000,
        "budget_cost_usd": 250.0,
        "tasks": [
            "Add usage meter aggregation job for API request counts",
            "Implement proration math for mid-cycle plan upgrades",
            "Move Stripe webhook handling onto the idempotent consumer",
            "Backfill historical usage rows into the billing ledger",
            "Write dunning sequence: retry schedule plus customer email",
            "Guard invoice finalisation against duplicate meter writes",
            "Extract billing rules into a versioned pricing catalog",
            "Add per-tenant rate ceilings to the metering middleware",
            "Fix rounding drift on sub-cent usage line items",
            "Introduce credits balance applied before card charge",
            "Add an internal endpoint to replay a day of meters",
            "Cover the proration path with property-based tests",
            "Split the billing service out of the monolith handler",
            "Instrument meter lag so late usage is visible before close",
            "Document the invoicing state machine for support",
            "Handle dispute webhooks without killing the invoice",
            "Retire the legacy seat-count sync cron",
            "Add a dry-run mode to the invoice generator",
        ],
    },
    {
        "title": "Cut cold-start latency on the query path",
        "description": "p95 for dashboard queries went from 4.2s to under 900ms.",
        "project": "lumen-etl",
        "budget_tokens": 3_000_000,
        "budget_cost_usd": 180.0,
        "tasks": [
            "Profile the query planner on the 30-day hot partition",
            "Add a materialised rollup for daily active workspaces",
            "Warm the shard cache on deploy instead of on first hit",
            "Push filter predicates down into the Parquet reader",
            "Kill the N+1 on workspace member lookups",
            "Tune TimescaleDB chunk interval from 7 days to 1 day",
            "Cache dashboard manifests behind a 60s TTL",
            "Rewrite the funnel query as a single windowed scan",
            "Add EXPLAIN capture to the slow-query sampler",
            "Drop the unused JSONB column from events_raw",
            "Batch the enrichment join against the customers table",
            "Move timezone bucketing out of application code",
            "Investigate tail latency spikes after vacuum",
            "Add a regression suite that asserts p95, not mean",
            "Cut serialisation cost with a streaming JSON encoder",
            "Precompile template SQL for the top 20 charts",
            "Split ingest and read pools onto separate connections",
            "Write up the latency budget for the query path",
        ],
    },
    {
        "title": "Harden auth and tenant isolation",
        "description": "Post-incident work: row-level security, token rotation, SCIM.",
        "project": "lumen-api",
        "budget_tokens": 2_500_000,
        "budget_cost_usd": 150.0,
        "tasks": [
            "Enforce row-level security on every tenant-scoped table",
            "Rotate refresh tokens on use and revoke the family on reuse",
            "Add SCIM user provisioning for Okta customers",
            "Close the IDOR on the report-share endpoint",
            "Require email verification before API key minting",
            "Scope service tokens to a single organisation",
            "Add an audit trail for permission changes",
            "Migrate session cookies to SameSite=strict",
            "Reject tokens issued before a membership downgrade",
            "Write the tenant-isolation test harness",
            "Audit every raw SQL query for missing org filters",
            "Add IP allowlisting for the enterprise tier",
            "Harden the password-reset rate limit",
            "Replace the legacy shared API secret in deploy configs",
            "Cover webhook signature verification edge cases",
            "Document the threat model for the share links",
        ],
    },
    {
        "title": "Self-serve onboarding revamp",
        "description": "Signup-to-first-dashboard conversion, wizard and empty states.",
        "project": "lumen-web",
        "budget_tokens": 2_000_000,
        "budget_cost_usd": 120.0,
        "tasks": [
            "Build the four-step workspace creation wizard",
            "Add sample dataset so the first chart is never empty",
            "Redesign the invite flow with role preselection",
            "Wire product tours to the feature flag service",
            "Fix the onboarding funnel drop at source connection",
            "Add progress persistence across wizard reloads",
            "Localise onboarding copy for ja-JP and de-DE",
            "Make the empty states actionable, not decorative",
            "Instrument funnel events with a typed helper",
            "Cut the signup form from nine fields to three",
            "Add a data-source health check before first query",
            "Retry failed seed imports with an inline banner",
            "Move the billing prompt after first value moment",
            "Audit keyboard focus order in the wizard",
            "Add a cancel path that keeps the workspace draft",
            "Test the onboarding journey in Playwright",
        ],
    },
    {
        "title": "Data quality and observability backlog",
        "description": "Alert rules, dbt tests, SLO dashboards, on-call noise.",
        "project": "lumen-infra",
        "budget_tokens": 1_500_000,
        "budget_cost_usd": 100.0,
        "tasks": [
            "Add dbt freshness tests on the marts layer",
            "Convert page-worthy alerts to an SLO burn-rate rule",
            "Fix the duplicate-span bug in the OTel collector",
            "Add backlog-depth alerting on the ingest queue",
            "Tag every dashboard with an owning team label",
            "Silence the flaky nightly replica-lag alert",
            "Move secrets out of deploy env into the vault",
            "Write runbooks for the top five paged incidents",
            "Add schema-drift detection on customer event streams",
            "Trim log volume on the hot ingest path",
            "Reconcile the metrics gap after the region failover",
            "Add a synthetic monitor for the export endpoint",
            "Automate the Terraform plan review in CI",
            "Instrument per-tenant ingest cost attribution",
            "Rotate the Grafana service accounts",
        ],
    },
    {
        "title": "Docs overhaul and SDK samples",
        "description": "Reference docs, quickstarts, and runnable examples per language.",
        "project": "lumen-docs",
        "budget_tokens": 1_000_000,
        "budget_cost_usd": 80.0,
        "tasks": [
            "Rewrite the quickstart around a real dataset",
            "Generate the REST reference from the OpenAPI spec",
            "Add runnable Python and TypeScript SDK samples",
            "Document pagination and idempotency semantics",
            "Fix 40+ broken internal links in the guide",
            "Add a troubleshooting page for common 4xx responses",
            "Record the five-minute intro walkthrough",
            "Move the changelog to release notes per version",
            "Add a cookbook for cohort and retention queries",
            "Review every code sample against the current API",
            "Document rate limits and the retry budget",
            "Add search synonyms for the old endpoint names",
            "Prune deprecated pages into an archive section",
            "Set up link checking in CI",
        ],
    },
]

AGENTS = [
    {"agent": "atlas", "harness": "pi", "source": "cli"},
    {"agent": "vega", "harness": "claude-code", "source": "sdk"},
    {"agent": "quill", "harness": "codex-cli", "source": "cli"},
    {"agent": "scout", "harness": "pi", "source": "sdk"},
    {"agent": "sentry-review", "harness": "github-actions", "source": "ci"},
]

MODELS = [
    ("claude-opus-5", 0.15, 0.75),
    ("claude-sonnet-4.5", 0.06, 0.18),
    ("gpt-5.6", 0.05, 0.22),
    ("gemini-3-pro", 0.035, 0.14),
    ("llama-4-maverick", 0.02, 0.06),
]

STATUSES = ["logged"] * 78 + ["failed"] * 9 + ["partial"] * 7 + ["dispatched"] * 6

# Marbles created with unknown cost/tokens, backfilled via the rollup endpoint.
ROLLUP_PENDING = 12


class ApiError(Exception):
    def __init__(self, status, body, method, path):
        self.status, self.body, self.method, self.path = status, body, method, path

    @property
    def missing_scope(self):
        try:
            msg = json.loads(self.body).get("error", "")
        except (ValueError, AttributeError):
            msg = self.body or ""
        if "missing required scope" in msg:
            return msg.split("missing required scope:")[-1].strip()
        return None

    def __str__(self):
        scope = self.missing_scope
        hint = f" (api key missing scope '{scope}')" if scope else ""
        return f"{self.method} {self.path} -> {self.status}{hint}: {self.body[:200]}"


class Client:
    def __init__(self, base_url, api_key, timeout=20):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout
        self.skipped = []

    def request(self, method, path, body=None, headers=None, optional=False):
        url = self.base_url + path
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(url, data=data, method=method)
        req.add_header("Authorization", f"Bearer {self.api_key}")
        req.add_header("Content-Type", "application/json")
        for k, v in (headers or {}).items():
            req.add_header(k, v)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read().decode()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as e:
            err = ApiError(e.code, e.read().decode(errors="replace"), method, path)
            if optional and e.code == 403:
                self.skipped.append(str(err))
                return None
            raise err
        except urllib.error.URLError as e:
            raise ApiError(0, str(e.reason), method, path)

    def get(self, path, params=None):
        if params:
            path = f"{path}?{urllib.parse.urlencode(params)}"
        return self.request("GET", path)


def slugify(text):
    return "".join(c if c.isalnum() else "-" for c in text.lower()).strip("-")


def pick_model(rng):
    total = sum(1 for _ in MODELS)
    idx = min(int(rng.random() * total), total - 1)
    # weight toward the two workhorses
    return MODELS[idx if rng.random() > 0.35 else rng.randint(0, 1)]


def build_marble(rng, spec, idx, when, objective_id):
    model, price_in, price_out = pick_model(rng)
    status = rng.choice(STATUSES) if spec.get("statusable", True) else "logged"
    tokens_in = int(rng.triangular(1_500, 90_000, 14_000))
    tokens_out = int(tokens_in * rng.uniform(0.08, 0.4))
    cost = round(tokens_in / 1_000 * price_in + tokens_out / 1_000 * price_out, 4)
    # failed runs still burn money; long jobs cost more
    if status == "failed":
        cost = round(cost * rng.uniform(0.3, 1.6), 4)
    duration_ms = int(rng.triangular(20_000, 2_400_000, 180_000))
    who = rng.choice(AGENTS)
    repo = slugify(spec["project"])
    branch = rng.choice(["main", "master", f"feat/{spec['branch']}-{rng.randint(100,999)}",
                         f"fix/{spec['branch']}-{rng.randint(100,999)}"])
    body = {
        "summary": spec["summary"],
        "model": model,
        "project": spec["project"],
        "agent": who["agent"],
        "harness": who["harness"],
        "source": who["source"],
        "status": status,
        "occurred_at": when.astimezone(timezone.utc).isoformat(),
        "metadata": {
            "repo": repo,
            "branch": branch,
            "commit": uuid.UUID(int=rng.getrandbits(128)).hex[:40],
            "files_changed": rng.randint(1, 14),
            "lines_added": rng.randint(4, 720),
            "lines_deleted": rng.randint(0, 310),
            "pr_number": rng.randint(400, 1450),
            "objective_theme": spec["theme"],
        },
    }
    if objective_id:
        body["objective_id"] = objective_id
    if status in ("logged", "dispatched"):
        body["cost_usd"] = cost
        body["tokens_input"] = tokens_in
        body["tokens_output"] = tokens_out
        body["duration_ms"] = duration_ms
    elif rng.random() < 0.5:
        body["cost_usd"] = cost
        body["tokens_input"] = tokens_in
        body["tokens_output"] = tokens_out
    return body


def generate_specs(rng, count):
    """Flatten objective task pools into `count` marble specs, round-robin so the
    jar stays balanced, then jitter the order."""
    pool, ri = [], 0
    while len(pool) < count:
        for obj in OBJECTIVES:
            for task in obj["tasks"]:
                if len(pool) >= count:
                    break
                prefix = rng.choice(["", "", "Fix: ", "Follow-up: ", "Harden "])
                spec = {
                    "summary": (prefix + task if prefix and rng.random() < 0.45 else task)[:200],
                    "project": obj["project"],
                    "theme": obj["title"],
                    "branch": slugify(task).split("-")[:3],
                }
                spec["branch"] = "-".join(spec["branch"])
                pool.append(spec)
        ri += 1
    rng.shuffle(pool)
    return pool[:count]


def spread_times(rng, count, days):
    """Weekday-weighted timestamps across the last `days`, newest first not required."""
    now = datetime.now(timezone.utc)
    out = []
    for _ in range(count):
        back = rng.random() ** 1.35 * days
        when = now - timedelta(days=back)
        when = when.replace(hour=int(rng.triangular(7, 23, 13)),
                            minute=rng.randint(0, 59), second=rng.randint(0, 59))
        if rng.random() < 0.2:  # weekend work is rarer
            when -= timedelta(days=when.weekday() + 1 if when.weekday() >= 5 else 0)
        out.append(when)
    return sorted(out, reverse=True)


def rng_for(key):
    """Stable per-marble RNG so backfilled usage is reproducible across runs."""
    digest = hashlib.md5(str(key).encode()).digest()
    return random.Random(int.from_bytes(digest[:8], "big"))


def iter_marbles(client, params=None, page=100):
    """Walk cursor pagination so callers can stream every matching marble."""
    cursor, done = None, set()
    while True:
        q = dict(params or {})
        q["limit"] = page
        if cursor:
            q["cursor"] = cursor
        res = client.get("/v1/marbles", q)
        items = res.get("items", [])
        for m in items:
            if m["id"] not in done:
                done.add(m["id"])
                yield m
        cursor = res.get("next_cursor")
        if not cursor or not items:
            return


def assign_existing(client, theme_to_id, page, run_id):
    """Bucket already-seeded marbles into objectives by metadata.objective_theme,
    and backfill any that were seeded without usage. Lets the objectives step run
    after the fact, once scopes allow. Only touches tagged marbles."""
    prices = {name: (pin, pout) for name, pin, pout in MODELS}
    moved = filled = seen = 0
    theme_project = {}
    for m in iter_marbles(client, None, page):
        meta = m.get("metadata") or {}
        if meta.get("objective_theme") not in theme_to_id:
            continue
        seen += 1
        if m.get("project_id"):
            theme_project.setdefault(meta["objective_theme"], m["project_id"])
        patch = {}
        target = theme_to_id[meta["objective_theme"]]
        if m.get("objective_id") != target:
            patch["objective_id"] = target
        if m.get("cost_usd") in (None, 0) and meta.get("usage_estimated") is False:
            price_in, price_out = prices.get(m["model"], (0.05, 0.15))
            tin = int(rng_for(m["id"]).triangular(2_000, 60_000, 12_000))
            tout = int(tin * rng_for(m["id"]).uniform(0.1, 0.35))
            roll = {"tokens_in": tin, "tokens_out": tout,
                    "cost_usd": round(tin / 1_000 * price_in + tout / 1_000 * price_out, 4),
                    "duration_ms": int(rng_for(m["id"]).triangular(30_000, 900_000, 150_000))}
            if client.request("POST", f"/v1/marbles/{m['id']}/rollups", roll, optional=True):
                filled += 1
        if patch and client.request("PATCH", f"/v1/marbles/{m['id']}", patch, optional=True):
            moved += 1
    # objectives carry no project until we link them from their marbles
    linked = 0
    for theme, oid in theme_to_id.items():
        pid = theme_project.get(theme)
        if not pid:
            continue
        cur = client.get(f"/v1/objectives/{oid}")
        if not cur.get("project_id"):
            if client.request("PATCH", f"/v1/objectives/{oid}", {"project_id": pid}, optional=True):
                linked += 1
    print(f"  scanned {seen} tagged marbles: {moved} assigned, {filled} usage backfilled, "
          f"{linked} objectives linked to a project")
    return moved


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base-url", default=os.environ.get("CORE_API_BASE_URL", "http://localhost:8080"))
    ap.add_argument("--api-key", default=os.environ.get("MARBLEJAR_API_KEY"))
    ap.add_argument("--count", type=int, default=100, help="number of marbles (default 100)")
    ap.add_argument("--days", type=int, default=21, help="history window to backdate into")
    ap.add_argument("--seed", type=int, default=7, help="deterministic generator seed")
    ap.add_argument("--run-id", default=datetime.now(timezone.utc).strftime("%m%d-%H%M"),
                    help="tag used for idempotency keys and metadata")
    ap.add_argument("--no-objectives", action="store_true", help="skip objective creation")
    ap.add_argument("--assign-only", action="store_true",
                    help="skip seeding; only bucket existing seeded marbles into objectives")
    ap.add_argument("--assign-existing", action="store_true",
                    help="after seeding, also re-bucket any pre-existing tagged marbles")
    ap.add_argument("--page", type=int, default=100, help="list page size for --assign-only")
    ap.add_argument("--dry-run", action="store_true", help="print payloads, POST nothing")
    args = ap.parse_args()

    rng = random.Random(args.seed)

    if args.dry_run:
        for s in generate_specs(rng, 3):
            print(json.dumps(build_marble(rng, s, 0, datetime.now(timezone.utc), None), indent=2))
        return 0

    if not args.api_key:
        ap.error("no api key: set MARBLEJAR_API_KEY or pass --api-key")

    client = Client(args.base_url, args.api_key)
    me = client.get("/v1/me")
    print(f"org={me['organization']['name']}  scopes={','.join(me['principal']['scopes'])}")

    created = {"objectives": [], "marbles": [], "updates": 0, "rollups": 0}

    # ---- 1. objectives (create-or-reuse, so re-runs do not duplicate) ----
    if not args.no_objectives:
        print("\n== objectives ==")
        existing = {o["title"]: o for o in client.get("/v1/objectives", {"limit": 100}).get("items", [])}
        for obj in OBJECTIVES:
            if obj["title"] in existing:
                o = existing[obj["title"]]
                print(f"  = {o['id'][:8]}  {obj['title']} (existing)")
                created["objectives"].append({"id": o["id"], "spec": obj})
                continue
            body = {
                "title": obj["title"],
                "description": obj["description"],
                "status": "open",
                "budget_tokens": obj["budget_tokens"],
                "budget_cost_usd": obj["budget_cost_usd"],
            }
            res = client.request("POST", "/v1/objectives", body, optional=True)
            if res:
                created["objectives"].append({"id": res["id"], "spec": obj})
                print(f"  + {res['id'][:8]}  {obj['title']}")
            else:
                created["objectives"].append({"id": None, "spec": obj})
                print(f"  ! skipped (scope)  {obj['title']}")
                break

    theme_to_id = {o["spec"]["title"]: o["id"] for o in created["objectives"] if o["id"]}

    # ---- 2. marbles -------------------------------------------------------
    specs = [] if args.assign_only else generate_specs(rng, args.count)
    print(f"\n== marbles ({len(specs)}) ==")
    times = spread_times(rng, args.count, args.days)
    rollup_slots = set() if args.assign_only else set(
        rng.sample(range(args.count), min(ROLLUP_PENDING, args.count)))
    ids = []
    for i, spec in enumerate(specs):
        body = build_marble(rng, spec, i, times[i], theme_to_id.get(spec["theme"]))
        if i in rollup_slots:
            for key in ("cost_usd", "tokens_input", "tokens_output", "duration_ms"):
                body.pop(key, None)
            body["metadata"]["usage_estimated"] = False
        idem = f"{args.run_id}-m{i:04d}"
        body["metadata"]["seed_tag"] = args.run_id
        body["metadata"]["idempotency_key"] = idem
        m = client.request("POST", "/v1/marbles", body,
                           headers={"Idempotency-Key": idem}, optional=False)
        mid = m["marble_id"]
        m = m["marble"]
        ids.append(mid)
        if i % 10 == 9 or i == len(specs) - 1:
            print(f"  {i + 1}/{len(specs)} logged (last: {spec['project']})")
        created["marbles"].append(m)

    if args.assign_only or args.assign_existing:
        if not theme_to_id:
            print("  no objectives visible; nothing to assign")
        else:
            print("\n== assign existing ==")
            assign_existing(client, theme_to_id, args.page, args.run_id)

    # ---- 3. updates: PATCH objectives and marbles ------------------------
    print("\n== updates (PATCH) ==")
    for n, o in enumerate(created["objectives"]):
        if not o["id"]:
            continue
        patch = {"status": ["in_progress", "in_progress", "shipped", "open", "paused", "in_progress"][n % 6]}
        if n % 3 == 0:
            patch["budget_cost_usd"] = round(o["spec"]["budget_cost_usd"] * 1.25, 2)
        if n % 4 == 1:
            patch["description"] = o["spec"]["description"] + " (re-scoped after planning review)"
        res = client.request("PATCH", f"/v1/objectives/{o['id']}", patch, optional=True)
        if res:
            created["updates"] += 1
            print(f"  ~ objective {o['spec']['title'][:34]:<34} {patch}")

    for n, mid in enumerate(ids[:12]):
        patch = None
        if n == 0:
            patch = {"summary": created["marbles"][n]["summary"] + " (retried after flaky fixture)"}
        elif n == 1:
            patch = {"status": "logged"}
        elif n == 2 and theme_to_id:
            patch = {"objective_id": next(iter(theme_to_id.values()))}
        elif n == 3:
            patch = {"metadata": {"reviewed_by": "sentry-review", "approved": True,
                                  "note": "backfilled during demo seed"}}
        elif n == 4:
            patch = {"clear_objective": True}
        if not patch:
            continue
        res = client.request("PATCH", f"/v1/marbles/{mid}", patch, optional=True)
        if res:
            created["updates"] += 1
            print(f"  ~ marble {mid[:8]}  {list(patch)[0]}")

    # ---- 4. rollup backfill ----------------------------------------------
    print("\n== rollup backfill ==")
    for n, i in enumerate(sorted(rollup_slots)):
        m = created["marbles"][i]
        model, price_in, price_out = pick_model(rng)
        tokens_in = int(rng.triangular(2_000, 60_000, 12_000))
        tokens_out = int(tokens_in * rng.uniform(0.1, 0.35))
        body = {
            "tokens_in": tokens_in,
            "tokens_out": tokens_out,
            "cost_usd": round(tokens_in / 1_000 * price_in + tokens_out / 1_000 * price_out, 4),
            "duration_ms": int(rng.triangular(30_000, 900_000, 150_000)),
            "trace_id": uuid.UUID(int=rng.getrandbits(128)).hex[:32],
            "metrics": {"tool_calls": rng.randint(2, 30), "cache_hit_ratio": round(rng.random(), 3)},
        }
        res = client.request("POST", f"/v1/marbles/{m['id']}/rollups", body, optional=True)
        if res:
            created["rollups"] += 1
        if n == 0:
            print(f"  → backfilled {len(rollup_slots)} cost-less marbles"
                  if res else "  ! rollups:write scope missing, skipped")

    # ---- 5. verify --------------------------------------------------------
    print("\n== state ==")
    status = client.get("/v1/jar/status")
    for key in ("marbles_total", "marbles_today", "marbles_last_hour", "cost_today_usd",
                "tokens_today", "open_objectives", "pending_dispatches", "streak_days"):
        if key in status:
            print(f"  {key}: {status[key]}")
    objectives = client.get("/v1/objectives", {"limit": 20}).get("items", [])
    for o in objectives[:10]:
        r = o.get("rollup") or {}
        print(f"  {o['status']:<12} {o['title'][:36]:<36} "
              f"marbles={r.get('marble_count', 0):<4} ${r.get('cost_usd', 0):<9} "
              f"tok={r.get('total_tokens', 0):<9} budget={r.get('budget_pct_cost')}%")
    recent = client.get("/v1/marbles", {"limit": 1}).get("items", [])
    print(f"  newest marble: {recent[0]['summary'][:64] if recent else 'n/a'}")

    print(f"\ndone: {len(created['marbles'])} marbles, {len(theme_to_id)} objectives, "
          f"{created['updates']} patches, {created['rollups']} rollups")
    print(f"idempotency tag: --run-id {args.run_id} (re-running with the same tag is a no-op)")
    if client.skipped:
        print("\nskipped for missing scopes:")
        for s in sorted(set(client.skipped))[:6]:
            print(f"  {s}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
