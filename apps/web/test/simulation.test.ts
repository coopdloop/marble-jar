import { describe, expect, it } from "vitest";
import { buildMarbleGraph, MAX_GRAPH_NODES } from "@/lib/graph";
import { MarbleSim, type StepOptions } from "@/components/graph/simulation";
import type { Marble } from "@/lib/types";

function marble(overrides: Partial<Marble> = {}): Marble {
  return {
    id: "m1",
    organization_id: "org1",
    project_id: null,
    objective_id: null,
    agent_id: null,
    created_by_user_id: null,
    summary: "did a thing",
    model: "gpt-5",
    tokens_in: 100,
    tokens_out: 200,
    cost_usd: 0.01,
    duration_ms: 5_000,
    trace_id: null,
    phoenix_trace_url: null,
    status: "logged",
    metadata: {},
    source: "sdk",
    occurred_at: "2026-09-15T10:00:00.000Z",
    created_at: "2026-09-15T10:00:00.000Z",
    updated_at: "2026-09-15T10:00:00.000Z",
    ...overrides,
  };
}

const VIEW = { width: 1200, height: 440 };
const STILL: StepOptions["pointer"] = { x: 0, y: 0, inside: false };

/** Three objectives, four agents and a couple of expensive outliers. */
function fixture(count: number): Marble[] {
  return Array.from({ length: count }, (_, i) =>
    marble({
      id: `m${i}`,
      objective_id: `o${i % 3}`,
      agent_id: `ag${Math.floor(i / 6) % 2}`,
      cost_usd: i % 9 === 0 ? 4.5 : 0.02 + i * 0.01,
      occurred_at: new Date(Date.parse("2026-09-15T08:00:00.000Z") + i * 9 * 60_000).toISOString(),
    }),
  );
}

function makeSim(marbles: Marble[], view = VIEW, animate = true) {
  const graph = buildMarbleGraph(marbles, view);
  const sim = new MarbleSim();
  sim.sync(graph, 0, animate);
  return { graph, sim, view };
}

/** Runs the layout forward and reports whether it was still awake at the end. */
function run(sim: MarbleSim, view: { width: number; height: number }, steps: number, opts: Partial<StepOptions> = {}): boolean {
  let awake = false;
  for (let i = 0; i < steps; i++) {
    awake = sim.step({
      width: view.width,
      height: view.height,
      pointer: STILL,
      hover: null,
      animate: true,
      ...opts,
    });
  }
  return awake;
}

/** Worst gap between any two marbles, as a fraction of their combined radii. */
function separation(sim: MarbleSim): number {
  const bodies = [...sim.bodies.values()];
  let worst = Number.POSITIVE_INFINITY;
  for (let i = 0; i < bodies.length; i++) {
    for (let j = i + 1; j < bodies.length; j++) {
      const a = bodies[i]!;
      const b = bodies[j]!;
      worst = Math.min(worst, Math.hypot(a.x - b.x, a.y - b.y) / (a.r + b.r));
    }
  }
  return worst;
}

function avg(values: number[]): number {
  return values.reduce((s, v) => s + v, 0) / values.length;
}

describe("marble simulation", () => {
  it("settles to rest inside the box, sparse or dense", () => {
    for (const view of [VIEW, { width: 380, height: 440 }]) {
      for (const count of [12, 110]) {
        const { sim } = makeSim(fixture(count), view);
        expect(run(sim, view, 1200)).toBe(false);
        const bodies = [...sim.bodies.values()];
        // Capacity is capped by both the node budget and the canvas area.
        expect(bodies.length).toBeLessThanOrEqual(Math.min(count, MAX_GRAPH_NODES));
        expect(bodies.length).toBeGreaterThan(0);

        for (const b of bodies) {
          expect(Number.isFinite(b.x)).toBe(true);
          expect(Number.isFinite(b.y)).toBe(true);
          expect(b.x).toBeGreaterThanOrEqual(b.r);
          expect(b.x).toBeLessThanOrEqual(view.width - b.r);
          expect(b.y).toBeGreaterThanOrEqual(b.r);
          expect(b.y).toBeLessThanOrEqual(view.height - b.r);
          // Once calm the simulation sleeps rather than grinding a CPU.
          expect(Math.abs(b.vx) + Math.abs(b.vy)).toBe(0);
        }
        expect(separation(sim)).toBeGreaterThanOrEqual(0.85);
      }
    }
  });

  it("gathers a thread towards the marble under the cursor", () => {
    const { graph, sim, view } = makeSim(fixture(24));
    run(sim, view, 500);
    const hubId = graph.edges.find((e) => e.kind === "thread")!.source;
    const neighbourIds = sim.neighboursOf(hubId).map((e) => (e.source === hubId ? e.target : e.source));
    const gap = () => {
      const hub = sim.bodies.get(hubId)!;
      return avg(neighbourIds.map((id) => Math.hypot(sim.bodies.get(id)!.x - hub.x, sim.bodies.get(id)!.y - hub.y)));
    };
    const before = gap();

    const hub = sim.bodies.get(hubId)!;
    const cursor = { x: hub.x, y: hub.y };
    sim.wake();
    run(sim, view, 90, { pointer: { ...cursor, inside: true }, hover: { kind: "node", id: hubId } });

    expect(gap()).toBeLessThan(before);
  });

  it("pulls the two marbles of a hovered line towards the cursor", () => {
    const { graph, sim, view } = makeSim(fixture(24));
    run(sim, view, 500);
    const edge = graph.edges.find((e) => e.kind === "thread")!;
    const a = sim.bodies.get(edge.source)!;
    const b = sim.bodies.get(edge.target)!;
    const cursor = { x: (a.x + b.x) / 2 + 70, y: (a.y + b.y) / 2 + 70 };
    const endsToCursor = () =>
      avg([
        Math.hypot(sim.bodies.get(edge.source)!.x - cursor.x, sim.bodies.get(edge.source)!.y - cursor.y),
        Math.hypot(sim.bodies.get(edge.target)!.x - cursor.x, sim.bodies.get(edge.target)!.y - cursor.y),
      ]);
    const before = endsToCursor();
    sim.wake();
    run(sim, view, 90, { pointer: { ...cursor, inside: true }, hover: { kind: "edge", id: edge.id } });

    expect(endsToCursor()).toBeLessThan(before);
  });

  it("ignores the cursor when motion is reduced", () => {
    const { sim, view } = makeSim(fixture(20));
    run(sim, view, 500);
    const snapshot = [...sim.bodies.values()].map((b) => [b.x, b.y]);
    const first = snapshot[0]!;
    sim.wake();

    run(sim, view, 200, {
      pointer: { x: first[0]!, y: first[1]!, inside: true },
      hover: { kind: "node", id: "m0" },
      animate: false,
    });

    const drift = avg(
      snapshot.map(([x, y], i) => {
        const b = [...sim.bodies.values()][i]!;
        return Math.hypot(b.x - x, b.y - y);
      }),
    );
    expect(drift).toBeLessThan(0.5);
  });

  it("keeps settled marbles put when a new one arrives", () => {
    const marbles = fixture(20);
    const { sim, view } = makeSim(marbles);
    run(sim, view, 500);
    const before = new Map([...sim.bodies].map(([id, b]) => [id, { x: b.x, y: b.y }]));

    const arrivals = marble({
      id: "new",
      objective_id: "o0",
      agent_id: "ag0",
      occurred_at: "2026-09-15T20:00:00.000Z",
    });
    sim.sync(
      buildMarbleGraph([...marbles, arrivals], { ...view, positions: before }),
      1000,
      true,
    );
    run(sim, view, 8);

    const drift = [...before].map(([id, p]) => {
      const b = sim.bodies.get(id)!;
      return Math.hypot(b.x - p.x, b.y - p.y);
    });
    expect(avg(drift)).toBeLessThan(4);
    expect(sim.bodies.get("new")?.scale).toBeLessThan(1);
  });

  it("wakes again when the cursor moves over a settled layout", () => {
    const { graph, sim, view } = makeSim(fixture(24));
    run(sim, view, 500);
    const target = graph.nodes.find((n) => n.id === "m5")!;
    const body = sim.bodies.get(target.id)!;
    const cursor = { x: body.x - 30, y: body.y, inside: true };
    const start = { x: body.x, y: body.y };
    // The component calls wake() from its pointermove handler; a sleeping
    // layout must come back to life.
    sim.wake();

    run(sim, view, 60, { pointer: cursor, hover: null });

    const moved = Math.hypot(sim.bodies.get(target.id)!.x - start.x, sim.bodies.get(target.id)!.y - start.y);
    expect(moved).toBeGreaterThan(0.5);
  });
});
