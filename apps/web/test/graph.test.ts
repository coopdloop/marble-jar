import { describe, expect, it } from "vitest";
import { buildMarbleGraph, MAX_GRAPH_NODES, type GraphEdge } from "@/lib/graph";
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

const VIEWPORT = { width: 1200, height: 440 };

function edgeBetween(edges: GraphEdge[], a: string, b: string) {
  return edges.find(
    (e) => (e.source === a && e.target === b) || (e.source === b && e.target === a),
  );
}

describe("thread links", () => {
  it("chains the steps of one objective in time order", () => {
    const graph = buildMarbleGraph(
      [
        marble({ id: "c", occurred_at: "2026-09-15T12:00:00.000Z", objective_id: "o1" }),
        marble({ id: "a", occurred_at: "2026-09-15T10:00:00.000Z", objective_id: "o1" }),
        marble({ id: "b", occurred_at: "2026-09-15T11:00:00.000Z", objective_id: "o1" }),
      ],
      VIEWPORT,
    );

    expect(graph.edges.map((e) => e.kind)).toEqual(["thread", "thread"]);
    expect(edgeBetween(graph.edges, "a", "b")).toBeDefined();
    expect(edgeBetween(graph.edges, "b", "c")).toBeDefined();
    expect(graph.nodes.find((n) => n.id === "a")?.step).toBe(1);
    expect(graph.nodes.find((n) => n.id === "c")?.step).toBe(3);
  });

  it("marks a step where the agent changed as a handoff", () => {
    const graph = buildMarbleGraph(
      [
        marble({ id: "a", objective_id: "o1", agent_id: "ag1", agent_name: "scout" }),
        marble({
          id: "b",
          objective_id: "o1",
          agent_id: "ag2",
          agent_name: "fixer",
          occurred_at: "2026-09-15T11:00:00.000Z",
        }),
      ],
      VIEWPORT,
    );

    const link = edgeBetween(graph.edges, "a", "b");
    expect(link?.kind).toBe("handoff");
    expect(link?.label).toContain("scout");
    expect(link?.label).toContain("fixer");
  });

  it("links one agent's path across two threads as a relay", () => {
    const graph = buildMarbleGraph(
      [
        marble({ id: "a", objective_id: "o1", agent_id: "ag1", agent_name: "fixer" }),
        marble({
          id: "b",
          objective_id: "o2",
          agent_id: "ag1",
          agent_name: "fixer",
          occurred_at: "2026-09-15T11:00:00.000Z",
        }),
      ],
      VIEWPORT,
    );

    const relay = edgeBetween(graph.edges, "a", "b");
    expect(relay?.kind).toBe("relay");
    expect(relay?.label).toContain("fixer");
  });

  it("groups unfiled work into a single thread", () => {
    const graph = buildMarbleGraph(
      [marble({ id: "a" }), marble({ id: "b", occurred_at: "2026-09-15T11:00:00.000Z" })],
      VIEWPORT,
    );
    expect(graph.groups).toHaveLength(1);
    expect(graph.groups[0]?.label).toBe("Unfiled");
    expect(graph.edges[0]?.kind).toBe("thread");
  });

  it("names a thread after its objective", () => {
    const graph = buildMarbleGraph(
      [marble({ id: "a", objective_id: "o1", agent_id: "ag1" })],
      { ...VIEWPORT, objectiveLabels: { o1: "Ship the CSV export" } },
    );
    expect(graph.groups[0]?.label).toBe("Ship the CSV export");
    expect(graph.nodes[0]?.groupLabel).toBe("Ship the CSV export");
  });

  it("stretches a link when a lot of time passed between steps", () => {
    const build = (hours: number) =>
      buildMarbleGraph(
        [
          marble({ id: "a", objective_id: "o1" }),
          marble({
            id: "b",
            objective_id: "o1",
            occurred_at: `2026-09-15T${String(10 + hours).padStart(2, "0")}:00:00.000Z`,
          }),
        ],
        VIEWPORT,
      ).edges[0]!.restLength;

    expect(build(8)).toBeGreaterThan(build(0));
  });

  it("never links a marble to itself or duplicates a pair", () => {
    const graph = buildMarbleGraph(
      [
        marble({ id: "a", objective_id: "o1", agent_id: "ag1" }),
        marble({
          id: "a",
          objective_id: "o1",
          agent_id: "ag1",
          occurred_at: "2026-09-15T11:00:00.000Z",
        }),
        marble({ id: "b", objective_id: "o2", agent_id: "ag1" }),
      ],
      VIEWPORT,
    );
    const pairs = graph.edges.map((e) => [e.source, e.target].sort().join("|"));
    expect(new Set(pairs).size).toBe(pairs.length);
    expect(graph.edges.every((e) => e.source !== e.target)).toBe(true);
  });
});

describe("windowing and layout", () => {
  it("keeps the newest tail when there are more marbles than nodes", () => {
    const many = Array.from({ length: MAX_GRAPH_NODES + 40 }, (_, i) =>
      marble({
        id: `m${i}`,
        objective_id: "o1",
        occurred_at: new Date(Date.parse("2026-09-15T10:00:00.000Z") + i * 60_000).toISOString(),
      }),
    );
    const graph = buildMarbleGraph(many, VIEWPORT);

    expect(graph.nodes).toHaveLength(MAX_GRAPH_NODES);
    const ids = new Set(graph.nodes.map((n) => n.id));
    expect(ids.has("m0")).toBe(false);
    expect(ids.has(`m${many.length - 1}`)).toBe(true);
  });

  it("reuses held positions so live updates do not reshuffle the picture", () => {
    const marbles = [
      marble({ id: "a", objective_id: "o1" }),
      marble({ id: "b", objective_id: "o1", occurred_at: "2026-09-15T11:00:00.000Z" }),
    ];
    const first = buildMarbleGraph(marbles, VIEWPORT);
    const positions = new Map(first.nodes.map((n) => [n.id, { x: n.x + 90, y: n.y - 40 }]));
    const second = buildMarbleGraph(marbles, { ...VIEWPORT, positions });

    for (const node of second.nodes) {
      const held = positions.get(node.id)!;
      expect(node.x).toBe(held.x);
      expect(node.y).toBe(held.y);
    }
  });

  it("is deterministic for identical input", () => {
    const marbles = [
      marble({ id: "a", objective_id: "o1", cost_usd: 0.4 }),
      marble({ id: "b", objective_id: "o2", agent_id: "ag1", cost_usd: 2 }),
      marble({ id: "c", objective_id: "o2", agent_id: "ag1", cost_usd: 0.02 }),
    ];
    const one = buildMarbleGraph(marbles, VIEWPORT);
    const two = buildMarbleGraph(marbles, VIEWPORT);
    expect(two.nodes.map((n) => [n.id, Math.round(n.x), Math.round(n.y)])).toEqual(
      one.nodes.map((n) => [n.id, Math.round(n.x), Math.round(n.y)]),
    );
    expect(two.edges.map((e) => e.id)).toEqual(one.edges.map((e) => e.id));
  });

  it("sizes marbles by cost and seeds them inside the viewport", () => {
    const graph = buildMarbleGraph(
      [
        marble({ id: "cheap", objective_id: "o1", cost_usd: 0.001 }),
        marble({ id: "rich", objective_id: "o1", cost_usd: 9 }),
      ],
      VIEWPORT,
    );
    const cheap = graph.nodes.find((n) => n.id === "cheap")!;
    const rich = graph.nodes.find((n) => n.id === "rich")!;
    expect(rich.radius).toBeGreaterThan(cheap.radius);
    for (const n of graph.nodes) {
      expect(n.x).toBeGreaterThan(0);
      expect(n.x).toBeLessThan(VIEWPORT.width);
      expect(n.y).toBeGreaterThan(0);
      expect(n.y).toBeLessThan(VIEWPORT.height);
    }
  });
});
