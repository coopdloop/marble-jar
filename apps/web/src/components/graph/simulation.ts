import type { GraphEdge, GraphNode, MarbleGraph } from "@/lib/graph";

/**
 * The force law behind the marble constellation.
 *
 * Deliberately not a general-purpose physics engine: there is no gravity, no
 * jar and nothing to throw. Marbles spring along their logical links, shoulder
 * each other aside, lean toward the cursor, and then come to rest. Once at
 * rest the simulation sleeps so a wall-mounted TV is not burning a core.
 */

export interface Body {
  id: string;
  x: number;
  y: number;
  vx: number;
  vy: number;
  r: number;
  /** Entrance animation, 0 → 1. */
  scale: number;
  born: number;
  node: GraphNode;
}

export interface StepOptions {
  width: number;
  height: number;
  pointer: { x: number; y: number; inside: boolean };
  hover: { kind: "node" | "edge"; id: string } | null;
  /** False for prefers-reduced-motion: positions still relax, nothing drifts. */
  animate: boolean;
}

const DAMPING = 0.88;
const MAX_SPEED = 2.4;
/** Below this speed a marble is treated as resting. */
const STICTION_SPEED = 0.06;
const STICTION_DRAG = 0.4;
const SPRING = 0.012;
const REPULSE = 0.2;
const COHESION = 0.0014;
const CENTRING = 0.0004;
const POINTER_FIELD = 78;
const POINTER_PUSH = 0.1;
const TUG = 0.07;
const SAG = 0.0035;
const ENTRANCE_RATE = 0.05;
const MARGIN = 10;

export class MarbleSim {
  bodies = new Map<string, Body>();

  private graph: MarbleGraph | null = null;
  private edgesByNode = new Map<string, GraphEdge[]>();
  private seeds = new Map<string, { x: number; y: number }>();
  private calmTicks = 0;
  private asleep = false;

  get nodeCount(): number {
    return this.bodies.size;
  }

  /** Replace the logical graph, keeping the position of marbles already drawn. */
  sync(graph: MarbleGraph, now: number, animate: boolean): void {
    this.graph = graph;
    this.edgesByNode = new Map();
    for (const e of graph.edges) {
      for (const id of [e.source, e.target]) {
        const bucket = this.edgesByNode.get(id);
        if (bucket) bucket.push(e);
        else this.edgesByNode.set(id, [e]);
      }
    }
    this.seeds = new Map(graph.groups.map((g) => [g.key, { x: g.cx, y: g.cy }]));

    const next = new Map<string, Body>();
    for (const node of graph.nodes) {
      const held = this.bodies.get(node.id);
      if (held) {
        held.node = node;
        held.r = node.radius;
        next.set(node.id, held);
        continue;
      }
      // A hair of deterministic jitter so a newly seeded thread does not look
      // like a grid, without the layout depending on Math.random().
      const jitter = (hash(node.id) % 20) / 20 - 1;
      next.set(node.id, {
        id: node.id,
        node,
        x: node.x,
        y: node.y,
        vx: animate ? jitter * 0.5 : 0,
        vy: animate ? -jitter * 0.4 : 0,
        r: node.radius,
        scale: animate ? 0 : 1,
        born: now,
      });
    }
    this.bodies = next;
    this.wake();
  }

  /** Rescale positions so a window resize does not reshuffle the picture. */
  scalePositions(sx: number, sy: number): void {
    for (const b of this.bodies.values()) {
      b.x *= sx;
      b.y *= sy;
    }
    for (const s of this.seeds.values()) {
      s.x *= sx;
      s.y *= sy;
    }
  }

  wake(): void {
    this.calmTicks = 0;
    this.asleep = false;
  }

  neighboursOf(id: string): GraphEdge[] {
    return this.edgesByNode.get(id) ?? [];
  }

  /**
   * Advance one fixed timestep. Returns true when something moved or is still
   * animating, which tells the renderer there is more to show.
   */
  step(opts: StepOptions): boolean {
    const graph = this.graph;
    const { width: w, height: h, pointer, hover, animate } = opts;
    if (!graph || graph.nodes.length === 0 || w <= 0 || h <= 0) return false;

    let animating = false;
    if (animate) {
      for (const b of this.bodies.values()) {
        if (b.scale < 1) {
          b.scale = Math.min(1, b.scale + ENTRANCE_RATE);
          animating = true;
        }
      }
    }

    const interacting = animate && (pointer.inside || hover !== null);
    if (this.asleep && !animating && !interacting) return false;

    const fx = new Map<string, number>();
    const fy = new Map<string, number>();
    const add = (id: string, ax: number, ay: number) => {
      fx.set(id, (fx.get(id) ?? 0) + ax);
      fy.set(id, (fy.get(id) ?? 0) + ay);
    };

    // Repulsion: linear falloff, stronger for bigger marbles. They shoulder
    // each other aside; they never fly apart.
    const nodes = graph.nodes;
    for (let i = 0; i < nodes.length; i++) {
      const a = nodes[i]!;
      const ba = this.bodies.get(a.id);
      if (!ba) continue;
      for (let j = i + 1; j < nodes.length; j++) {
        const b = nodes[j]!;
        const bb = this.bodies.get(b.id)!;
        const dx = bb.x - ba.x;
        const dy = bb.y - ba.y;
        const reach = 62 + a.radius + b.radius;
        if (Math.abs(dx) > reach || Math.abs(dy) > reach) continue;
        const d2 = dx * dx + dy * dy;
        if (d2 > reach * reach) continue;
        const d = Math.sqrt(d2) || 0.01;
        const push = REPULSE * (1 - d / reach) * ((a.radius + b.radius) / 24);
        const ux = dx / d;
        const uy = dy / d;
        add(a.id, -ux * push, -uy * push);
        add(b.id, ux * push, uy * push);

        // Glass does not share space: resolve real overlaps positionally so a
        // dense thread never smudges into one blob.
        const touching = (a.radius + b.radius) * 0.94;
        if (d < touching && d > 0.01) {
          const shift = (touching - d) * 0.4;
          ba.x -= ux * shift;
          ba.y -= uy * shift;
          bb.x += ux * shift;
          bb.y += uy * shift;
        }
      }
    }

    // Springs along the logical links.
    for (const e of graph.edges) {
      const a = this.bodies.get(e.source);
      const b = this.bodies.get(e.target);
      if (!a || !b) continue;
      const dx = b.x - a.x;
      const dy = b.y - a.y;
      const d = Math.hypot(dx, dy) || 0.01;
      const pull = SPRING * (d - e.restLength);
      const ux = dx / d;
      const uy = dy / d;
      add(e.source, ux * pull, uy * pull);
      add(e.target, -ux * pull, -uy * pull);
    }

    // Each thread is anchored to its own pocket, and the whole constellation is
    // held gently towards the middle of the canvas.
    for (const node of nodes) {
      const body = this.bodies.get(node.id);
      if (!body) continue;
      const seed = this.seeds.get(node.groupKey);
      if (seed) add(node.id, (seed.x - body.x) * COHESION, (seed.y - body.y) * COHESION);
      add(node.id, (w / 2 - body.x) * CENTRING, (h / 2 - body.y) * CENTRING);
    }

    if (interacting) {
      // What you point at is held, not shoved: the hovered marble, its
      // neighbours and the ends of a hovered line are exempt from the wake.
      const held = new Set<string>();
      if (hover?.kind === "node") {
        held.add(hover.id);
        for (const e of this.neighboursOf(hover.id)) {
          held.add(e.source === hover.id ? e.target : e.source);
        }
      } else if (hover?.kind === "edge") {
        const e = graph.edges.find((x) => x.id === hover.id);
        if (e) held.add(e.source).add(e.target);
      }

      if (pointer.inside) {
        for (const body of this.bodies.values()) {
          if (held.has(body.id)) continue;
          const dx = body.x - pointer.x;
          const dy = body.y - pointer.y;
          const d = Math.hypot(dx, dy);
          if (d > POINTER_FIELD || d < 0.001) continue;
          const push = POINTER_PUSH * (1 - d / POINTER_FIELD);
          add(body.id, (dx / d) * push, (dy / d) * push);
        }
      }

      // Hovering a marble gathers its thread towards it.
      if (hover?.kind === "node") {
        const hub = this.bodies.get(hover.id);
        if (hub) {
          for (const e of this.neighboursOf(hover.id)) {
            const nb = this.bodies.get(e.source === hover.id ? e.target : e.source);
            if (!nb) continue;
            const dx = hub.x - nb.x;
            const dy = hub.y - nb.y;
            const d = Math.hypot(dx, dy) || 1;
            add(nb.id, (dx / d) * TUG, (dy / d) * TUG);
          }
        }
      }

      // Hovering a line draws its two marbles towards the cursor, which is how
      // you see at a glance which work the connection joins.
      if (hover?.kind === "edge") {
        const e = graph.edges.find((x) => x.id === hover.id);
        if (e) {
          for (const id of [e.source, e.target]) {
            const body = this.bodies.get(id);
            if (!body) continue;
            add(id, (pointer.x - body.x) * SAG, (pointer.y - body.y) * SAG);
          }
        }
      }
    }

    let motion = 0;
    let peak = 0;
    for (const body of this.bodies.values()) {
      body.vx = (body.vx + (fx.get(body.id) ?? 0)) * DAMPING;
      body.vy = (body.vy + (fy.get(body.id) ?? 0)) * DAMPING;
      let speed = Math.hypot(body.vx, body.vy);
      if (speed > MAX_SPEED) {
        body.vx = (body.vx / speed) * MAX_SPEED;
        body.vy = (body.vy / speed) * MAX_SPEED;
        speed = MAX_SPEED;
      }
      // Granular friction: marbles come to rest in a pile instead of sliding
      // forever on the residue of a balanced force.
      if (speed < STICTION_SPEED) {
        body.vx *= STICTION_DRAG;
        body.vy *= STICTION_DRAG;
      }
      body.x += body.vx;
      body.y += body.vy;


      motion += Math.abs(body.vx) + Math.abs(body.vy);
      peak = Math.max(peak, Math.abs(body.vx), Math.abs(body.vy));
    }

    // The constellation is fitted to its box rather than crammed against it: an
    // over-full layout compresses towards the centre, so a dense queue stays
    // legible instead of grinding marbles into the walls.
    let minX = Number.POSITIVE_INFINITY;
    let maxX = Number.NEGATIVE_INFINITY;
    let minY = Number.POSITIVE_INFINITY;
    let maxY = Number.NEGATIVE_INFINITY;
    for (const body of this.bodies.values()) {
      minX = Math.min(minX, body.x - body.r);
      maxX = Math.max(maxX, body.x + body.r);
      minY = Math.min(minY, body.y - body.r);
      maxY = Math.max(maxY, body.y + body.r);
    }
    const wantX = w - MARGIN * 2;
    const wantY = h - MARGIN * 2;
    const compressX = maxX - minX > wantX ? wantX / (maxX - minX) : 1;
    const compressY = maxY - minY > wantY ? wantY / (maxY - minY) : 1;
    if (compressX < 1 || compressY < 1) {
      const cx = (minX + maxX) / 2;
      const cy = (minY + maxY) / 2;
      const fx = 1 - (1 - compressX) * 0.3;
      const fy = 1 - (1 - compressY) * 0.3;
      for (const body of this.bodies.values()) {
        body.x = cx + (body.x - cx) * fx;
        body.y = cy + (body.y - cy) * fy;
      }
    }
    for (const body of this.bodies.values()) {
      const lo = MARGIN + body.r;
      if (body.x < lo) {
        body.x = lo;
        if (body.vx < 0) body.vx = 0;
      } else if (body.x > w - lo) {
        body.x = w - lo;
        if (body.vx > 0) body.vx = 0;
      }
      if (body.y < lo) {
        body.y = lo;
        if (body.vy < 0) body.vy = 0;
      } else if (body.y > h - lo) {
        body.y = h - lo;
        if (body.vy > 0) body.vy = 0;
      }
    }

    const average = motion / this.bodies.size;
    // A few px per second is beneath notice, and chasing an exact equilibrium
    // would keep the canvas hot forever on a dense queue.
    const settled = average < 0.05 && peak < 0.18 && !animating && !interacting;
    this.calmTicks = settled ? this.calmTicks + 1 : 0;
    if (this.calmTicks > 60) {
      this.asleep = true;
      for (const body of this.bodies.values()) {
        body.vx = 0;
        body.vy = 0;
      }
    }
    return !this.asleep;
  }
}

function hash(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}
