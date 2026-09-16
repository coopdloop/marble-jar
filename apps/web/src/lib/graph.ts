import type { Marble } from "./types";
import { colorForKey, formatDuration, marbleColor, marbleRadius, median } from "./utils";

/**
 * Turns finished work into a constellation instead of a pile.
 *
 * Every marble is a node; every line is a statement about how two units of
 * work are logically related:
 *
 *  - `thread`   — the next step of the same objective (or project, or agent).
 *  - `handoff`  — a thread step where the agent changed: work was passed on.
 *  - `relay`    — the same agent's work crossing from one thread to another.
 *
 * The whole module is pure and deterministic so the linking rules can be
 * unit-tested without a canvas.
 */

/** Newest tail of the queue is kept; the sim stays cheap on a wall-mounted TV. */
export const MAX_GRAPH_NODES = 110;

export type GraphEdgeKind = "thread" | "handoff" | "relay";

export interface GraphGroup {
  key: string;
  label: string;
  hue: string;
  marbleCount: number;
  costUsd: number;
  /** Deterministic seed centre — the simulation drifts away from it. */
  cx: number;
  cy: number;
}

export interface GraphNode {
  id: string;
  marble: Marble;
  groupKey: string;
  groupLabel: string;
  /** 1-based position along its own thread, for tooltips and labels. */
  step: number;
  stepCount: number;
  radius: number;
  color: string;
  hue: string;
  glow: boolean;
  x: number;
  y: number;
}

export interface GraphEdge {
  id: string;
  kind: GraphEdgeKind;
  source: string;
  target: string;
  restLength: number;
  hue: string;
  label: string;
}

export interface MarbleGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
  groups: GraphGroup[];
}

export interface BuildOptions {
  width: number;
  height: number;
  /** Objective id → title, so threads can be named instead of uuid-sliced. */
  objectiveLabels?: Record<string, string>;
  /** Live positions from a previous build, so rebuilds do not reset layout. */
  positions?: Map<string, { x: number; y: number }>;
  maxNodes?: number;
}

const HOUR_MS = 3_600_000;
/** Canvas pixels each marble needs to keep a thread readable. */
const AREA_PER_MARBLE = 3_800;

/** Golden angle: spreads seeded nodes without the clumping of Math.random(). */
const GOLDEN_ANGLE = 2.399963229728653;

function occurredMs(m: Marble): number {
  const t = Date.parse(m.occurred_at || m.created_at);
  return Number.isFinite(t) ? t : 0;
}

function shortId(id: string): string {
  return `#${id.slice(0, 6)}`;
}

function agentKey(m: Marble): string | null {
  const key = m.agent_id ?? m.agent_name ?? null;
  return key ? String(key) : null;
}

function agentLabel(m: Marble, fallback = "unattributed"): string {
  return m.agent_name ?? (m.agent_id ? shortId(m.agent_id) : fallback);
}

interface Grouping {
  key: string;
  label: string;
}

/**
 * A thread is the chain of work belonging to one thing. We nest down from the
 * strongest filing signal to the weakest: objective, then project, then agent,
 * and finally one shared "Unfiled" thread — which is deliberately the least
 * pretty picture on the page, because unfiled work has no story to follow.
 */
function groupOf(m: Marble, objectiveLabels: Record<string, string>): Grouping {
  if (m.objective_id) {
    return {
      key: `obj:${m.objective_id}`,
      label: objectiveLabels[m.objective_id] ?? "Objective",
    };
  }
  if (m.project_id || m.project_name) {
    return { key: `prj:${m.project_id ?? m.project_name}`, label: m.project_name ?? "Project" };
  }
  const agent = agentKey(m);
  if (agent) return { key: `agent:${agent}`, label: agentLabel(m) };
  return { key: "unfiled", label: "Unfiled" };
}

function clamp(v: number, min: number, max: number): number {
  return Math.min(Math.max(v, min), max);
}

export function buildMarbleGraph(marbles: Marble[], opts: BuildOptions): MarbleGraph {
  const { width, height, objectiveLabels = {}, positions, maxNodes = MAX_GRAPH_NODES } = opts;
  const w = Math.max(width, 1);
  const h = Math.max(height, 1);

  // Chronological order is what makes a chain read as a sequence of steps.
  const ordered = [...marbles].sort(
    (a, b) => occurredMs(a) - occurredMs(b) || a.id.localeCompare(b.id),
  );
  // A short canvas cannot hold a long queue: capacity scales with the area so
  // marbles stay legible instead of grinding against each other.
  const capacity = Math.min(maxNodes, Math.max(30, Math.floor((w * h) / AREA_PER_MARBLE)));
  const window = ordered.length > capacity ? ordered.slice(-capacity) : ordered;

  const costs = window.map((m) => m.cost_usd ?? 0).filter((c) => c > 0);
  const maxCost = costs.length ? Math.max(...costs) : 0;
  const medianCost = median(costs);

  // Crowding shrinks every marble proportionally: relative cost still reads,
  // but a busy day fits the same hero instead of pressing on the borders.
  const density = (w * h) / Math.max(window.length, 1);
  const sizeScale = clamp(Math.sqrt(density) / 62, 0.6, 1);

  // --- group, in chronological order within each group ---
  const grouped = new Map<string, Marble[]>();
  const groupMeta = new Map<string, Grouping>();
  for (const m of window) {
    const g = groupOf(m, objectiveLabels);
    groupMeta.set(g.key, g);
    const bucket = grouped.get(g.key);
    if (bucket) bucket.push(m);
    else grouped.set(g.key, [m]);
  }

  // --- seed layout: one pocket per thread, arranged around the viewport ---
  const keys = [...grouped.keys()];
  const spread = keys.length > 1 ? 1 : 0;
  const rx = w * 0.3;
  const ry = h * 0.22;
  const groups: GraphGroup[] = keys.map((key, i) => {
    const meta = groupMeta.get(key)!;
    const members = grouped.get(key)!;
    const angle = -Math.PI / 2 + (i * 2 * Math.PI) / Math.max(keys.length, 1);
    const cx = w / 2 + Math.cos(angle) * rx * spread;
    const cy = h / 2 + Math.sin(angle) * ry * spread;
    return {
      key,
      label: meta.label,
      hue: colorForKey(key),
      marbleCount: members.length,
      costUsd: members.reduce((sum, m) => sum + (m.cost_usd ?? 0), 0),
      cx,
      cy,
    };
  });
  const nodes: GraphNode[] = [];
  const nodeById = new Map<string, GraphNode>();
  for (const group of groups) {
    const members = grouped.get(group.key)!;
    members.forEach((m, i) => {
      const held = positions?.get(m.id);
      // Deterministic spiral around the thread's pocket, so a rebuild without
      // cached positions still lands in the same shape.
      const a = i * GOLDEN_ANGLE;
      const rad = 16 + 11 * Math.sqrt(i);
      const node: GraphNode = {
        id: m.id,
        marble: m,
        groupKey: group.key,
        groupLabel: group.label,
        step: i + 1,
        stepCount: members.length,
        radius: marbleRadius(m, maxCost || m.cost_usd || 1) * sizeScale,
        color: marbleColor(m),
        hue: group.hue,
        glow: medianCost > 0 && (m.cost_usd ?? 0) > medianCost * 3,
        x: held?.x ?? clamp(group.cx + Math.cos(a) * rad, 20, w - 20),
        y: held?.y ?? clamp(group.cy + Math.sin(a) * rad, 20, h - 20),
      };
      nodes.push(node);
      nodeById.set(m.id, node);
    });
  }

  const edges: GraphEdge[] = [];
  const seenPair = new Set<string>();
  const pairKey = (a: string, b: string) => (a < b ? `${a}|${b}` : `${b}|${a}`);

  const pushEdge = (edge: GraphEdge) => {
    if (edge.source === edge.target) return;
    const key = pairKey(edge.source, edge.target);
    if (seenPair.has(key)) return;
    seenPair.add(key);
    edges.push(edge);
  };

  // --- thread edges: consecutive steps inside one group ---
  for (const group of groups) {
    const members = grouped.get(group.key)!;
    for (let i = 1; i < members.length; i++) {
      const prev = members[i - 1]!;
      const next = members[i]!;
      const gap = Math.max(occurredMs(next) - occurredMs(prev), 0);
      const handoff = agentKey(prev) !== agentKey(next);
      const a = nodeById.get(prev.id)!;
      const b = nodeById.get(next.id)!;
      // Time between steps is drawn as distance: a thread that sat idle for a
      // day should look longer than one resolved in four minutes.
      const stretch = clamp(gap / HOUR_MS, 0, 6) * 9;
      pushEdge({
        id: `e:${a.id}->${b.id}`,
        kind: handoff ? "handoff" : "thread",
        source: a.id,
        target: b.id,
        restLength: a.radius + b.radius + (handoff ? 42 : 26) * sizeScale + stretch,
        hue: handoff ? "#fbbf24" : group.hue,
        label: handoff
          ? `${group.label} · step ${i + 1} of ${members.length} — handed from ${agentLabel(prev)} to ${agentLabel(next)}${gap ? ` after ${formatDuration(gap)}` : ""}`
          : `${group.label} · step ${i + 1} of ${members.length} — ${agentLabel(next)} again${gap ? ` after ${formatDuration(gap)}` : ""}`,
      });
    }
  }

  // --- relay edges: one agent's own path across threads ---
  const anchors = new Map<string, { key: string; node: GraphNode }[]>();
  for (const group of groups) {
    const seen = new Set<string>();
    for (const m of grouped.get(group.key)!) {
      const agent = agentKey(m);
      if (!agent || seen.has(agent)) continue;
      // Anchor on where the agent *entered* the thread, not every step in it.
      seen.add(agent);
      const bucket = anchors.get(agent) ?? [];
      bucket.push({ key: group.key, node: nodeById.get(m.id)! });
      anchors.set(agent, bucket);
    }
  }
  for (const [agent, chain] of anchors) {
    for (let i = 1; i < chain.length; i++) {
      const from = chain[i - 1]!;
      const to = chain[i]!;
      const label = `${agentLabel(from.node.marble, agent)} moves from ${groupMeta.get(from.key)!.label} to ${groupMeta.get(to.key)!.label}`;
      pushEdge({
        id: `e:${from.node.id}->${to.node.id}`,
        kind: "relay",
        source: from.node.id,
        target: to.node.id,
        restLength: 130 + (from.node.radius + to.node.radius) / 2,
        hue: "#94a3b8",
        label,
      });
    }
  }

  // Keep the picture legible: relays are the least important link.
  const relays = edges.filter((e) => e.kind === "relay");
  if (relays.length > nodes.length * 0.5) {
    const budget = Math.floor(nodes.length * 0.5);
    const keep = new Set(relays.slice(-budget).map((e) => e.id));
    for (let i = edges.length - 1; i >= 0; i--) {
      const e = edges[i]!;
      if (e.kind === "relay" && !keep.has(e.id)) edges.splice(i, 1);
    }
  }

  return { nodes, edges, groups };
}
