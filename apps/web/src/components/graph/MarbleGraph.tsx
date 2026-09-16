import { useEffect, useMemo, useRef, useState } from "react";
import { buildMarbleGraph, type GraphEdge, type GraphNode, type MarbleGraph as Graph } from "@/lib/graph";
import type { Marble } from "@/lib/types";
import { colorForKey, formatCost, formatRelative } from "@/lib/utils";
import { useUiStore } from "@/stores/useAppStore";
import { MarbleSim, type Body } from "./simulation";

/**
 * The brand moment, restated: finished work is not a pile, it is a set of
 * threads. Marbles are nodes, and every line between them states how two units
 * of work are logically related (`lib/graph.ts` owns the rules).
 *
 * Nothing is grabbable any more. The layout settles, sleeps, and wakes only
 * under the cursor: nearby marbles part, a hovered marble gathers its thread, a
 * hovered line sags towards the pointer while its two marbles lean in. Clicking
 * a marble opens the detail sheet.
 */

const ARRIVAL_INTERVAL_MS = 240;
const STEP_MS = 1000 / 60;
const HIT_EDGE_TOLERANCE = 6;

interface MarbleGraphProps {
  marbles: Marble[];
  /** Objective id → title, used to name threads in labels and tooltips. */
  objectiveLabels?: Record<string, string>;
  onMarbleClick?: (id: string) => void;
  className?: string;
}

interface Hover {
  kind: "node" | "edge";
  id: string;
  title: string;
  subtitle: string;
}

interface Ink {
  label: string;
  labelDim: string;
  ring: string;
  /** Pill behind a thread name, so labels survive a busy layout. */
  backing: string;
  /** Lines lose contrast on a light canvas; this buys it back. */
  edge: number;
}

const INK: Record<"dark" | "light", Ink> = {
  dark: {
    label: "226,232,240",
    labelDim: "148,163,184",
    ring: "255,255,255",
    backing: "15,19,25",
    edge: 1,
  },
  light: {
    label: "15,23,42",
    labelDim: "100,116,139",
    ring: "15,23,42",
    backing: "250,249,246",
    edge: 1.7,
  },
};

/** A status ring, so dispatch state reads without a legend. */
const STATUS_TINT: Record<string, string> = {
  dispatched: "52,211,153",
  succeeded: "52,211,153",
  failed: "248,113,113",
  dead_lettered: "248,113,113",
  pending: "251,191,36",
  running: "251,191,36",
  retrying: "251,191,36",
};

const EDGE_TITLE: Record<GraphEdge["kind"], string> = {
  thread: "Same thread",
  handoff: "Handoff",
  relay: "Shared agent",
};

export function MarbleGraph({
  marbles,
  objectiveLabels,
  onMarbleClick,
  className,
}: MarbleGraphProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const tooltipRef = useRef<HTMLDivElement>(null);
  const simRef = useRef<MarbleSim | null>(null);
  simRef.current ??= new MarbleSim();
  const graphRef = useRef<Graph | null>(null);
  const pointerRef = useRef({ x: 0, y: 0, inside: false });
  const hoverRef = useRef<Hover | null>(null);
  const pressRef = useRef<{ x: number; y: number } | null>(null);
  const clickRef = useRef(onMarbleClick);

  const theme = useUiStore((s) => s.theme);
  const pendingArrivals = useUiStore((s) => s.pendingArrivals);
  const dequeueArrival = useUiStore((s) => s.dequeueArrival);
  const [reduceMotion] = useState(
    () => window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false,
  );
  const [size, setSize] = useState({ w: 0, h: 0 });
  const sim = simRef.current;
  const [hover, setHover] = useState<Hover | null>(null);
  const [stats, setStats] = useState({ nodes: 0, edges: 0, threads: 0 });

  // Marbles the socket just pushed stay hidden until the graph reveals them one
  // at a time, so live work arrives as a paced rhythm, not a jump-cut.
  const pendingIds = useMemo(() => new Set(pendingArrivals.map((m) => m.id)), [pendingArrivals]);
  const visible = useMemo(
    () => marbles.filter((m) => !pendingIds.has(m.id)),
    [marbles, pendingIds],
  );
  const labelKey = useMemo(
    () => Object.entries(objectiveLabels ?? {}).map(([k, v]) => `${k}=${v}`).join(","),
    [objectiveLabels],
  );
  // Stable identity: callers may pass a fresh object every render, which must
  // not be read as "the threads were renamed".
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const labels = useMemo(() => objectiveLabels ?? {}, [labelKey]);

  // ---- measure ----
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const apply = () => {
      const w = Math.round(container.clientWidth);
      const h = Math.round(container.clientHeight);
      setSize((prev) => (prev.w === w && prev.h === h ? prev : { w, h }));
    };
    apply();
    const observer = new ResizeObserver(apply);
    observer.observe(container);
    return () => observer.disconnect();
  }, []);

  // ---- keep the picture's proportions on resize, before anything reseeds ----
  const previousSize = useRef(size);
  useEffect(() => {
    const prev = previousSize.current;
    previousSize.current = size;
    if (prev.w && prev.h && size.w && size.h && (prev.w !== size.w || prev.h !== size.h)) {
      sim.scalePositions(size.w / prev.w, size.h / prev.h);
    }
  }, [size, sim]);

  // ---- rebuild the graph when the visible set, its labels or the viewport change ----
  useEffect(() => {
    if (size.w === 0 || size.h === 0) return;
    const positions = new Map<string, { x: number; y: number }>();
    for (const [id, body] of sim.bodies) positions.set(id, { x: body.x, y: body.y });

    const graph = buildMarbleGraph(visible, {
      width: size.w,
      height: size.h,
      objectiveLabels: labels,
      positions,
    });
    graphRef.current = graph;
    sim.sync(graph, performance.now(), !reduceMotion);
    setStats({ nodes: graph.nodes.length, edges: graph.edges.length, threads: graph.groups.length });
  }, [visible, labels, size, reduceMotion, sim]);

  // ---- reveal queued arrivals ----
  useEffect(() => {
    if (pendingArrivals.length === 0) return;
    const timer = setInterval(() => dequeueArrival(), ARRIVAL_INTERVAL_MS);
    return () => clearInterval(timer);
  }, [pendingArrivals.length, dequeueArrival]);

  // ---- render + interaction loop. Data lives in refs so this never restarts
  // when marbles arrive; only the theme and motion preference rebuild it.
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const ink = INK[theme === "dark" ? "dark" : "light"];
    let raf = 0;
    let last = performance.now();
    let accumulator = 0;

    const dpr = window.devicePixelRatio || 1;
    canvas.width = Math.round(size.w * dpr);
    canvas.height = Math.round(size.h * dpr);
    canvas.style.width = `${size.w}px`;
    canvas.style.height = `${size.h}px`;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    const frame = (now: number) => {
      accumulator += Math.min(now - last, 120);
      last = now;
      while (accumulator >= STEP_MS) {
        setHoverIfChanged(pick(graphRef.current, sim, pointerRef.current));
        sim.step({
          width: size.w,
          height: size.h,
          pointer: pointerRef.current,
          hover: hoverRef.current ? { kind: hoverRef.current.kind, id: hoverRef.current.id } : null,
          animate: !reduceMotion,
        });
        accumulator -= STEP_MS;
      }
      render(ctx, graphRef.current, sim, {
        ink,
        hover: hoverRef.current,
        pointer: pointerRef.current,
        now,
        reduceMotion,
        width: size.w,
        height: size.h,
        tooltip: tooltipRef.current,
      });
      raf = requestAnimationFrame(frame);
    };
    raf = requestAnimationFrame(frame);

    const toLocal = (ev: PointerEvent | MouseEvent) => {
      const rect = canvas.getBoundingClientRect();
      return { x: ev.clientX - rect.left, y: ev.clientY - rect.top };
    };
    const onPointerMove = (ev: PointerEvent) => {
      const { x, y } = toLocal(ev);
      pointerRef.current = { x, y, inside: true };
      sim.wake();
    };
    const onPointerLeave = () => {
      pointerRef.current.inside = false;
      sim.wake();
    };
    const onPointerDown = (ev: PointerEvent) => {
      pressRef.current = toLocal(ev);
    };
    const onClick = (ev: MouseEvent) => {
      const handler = clickRef.current;
      if (!handler) return;
      const { x, y } = toLocal(ev);
      const press = pressRef.current;
      if (press && Math.hypot(x - press.x, y - press.y) > 6) return;
      const hit = pick(graphRef.current, sim, { x, y, inside: true });
      if (hit?.kind === "node") handler(hit.id);
    };

    canvas.addEventListener("pointermove", onPointerMove);
    canvas.addEventListener("pointerleave", onPointerLeave);
    canvas.addEventListener("pointerdown", onPointerDown);
    canvas.addEventListener("click", onClick);

    return () => {
      cancelAnimationFrame(raf);
      canvas.removeEventListener("pointermove", onPointerMove);
      canvas.removeEventListener("pointerleave", onPointerLeave);
      canvas.removeEventListener("pointerdown", onPointerDown);
      canvas.removeEventListener("click", onClick);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [theme, reduceMotion, size, sim]);

  function setHoverIfChanged(next: Hover | null) {
    const current = hoverRef.current;
    if (current?.id === next?.id && current?.kind === next?.kind) return;
    hoverRef.current = next;
    setHover(next);
    const canvas = canvasRef.current;
    if (canvas) canvas.style.cursor = next?.kind === "node" ? "pointer" : "default";
    sim.wake();
  }

  return (
    <figure className={`flex h-full flex-col ${className ?? ""}`}>
      <div ref={containerRef} className="relative min-h-0 flex-1">
        <canvas
          ref={canvasRef}
          role="img"
          aria-label={`${stats.nodes} marbles joined by ${stats.edges} logical links across ${stats.threads} threads`}
        />
        <div
          ref={tooltipRef}
          className="pointer-events-none absolute left-0 top-0 w-64 rounded-lg border border-white/10 bg-card/90 p-2.5 opacity-0 shadow-xl backdrop-blur-md transition-opacity duration-150"
        >
          {hover ? (
            <>
              <div className="truncate text-xs font-medium">{hover.title}</div>
              <div className="mt-0.5 text-[11px] leading-snug text-muted-foreground">
                {hover.subtitle}
              </div>
            </>
          ) : null}
        </div>
      </div>

      <figcaption className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1.5 px-1 pt-2 text-[11px] text-muted-foreground">
        <span className="flex flex-wrap items-center gap-4">
          <Legend swatch="solid" label="next step of the same thread" />
          <Legend swatch="dashed" label="handed to a different agent" />
          <Legend swatch="dotted" label="same agent, another thread" />
        </span>
        <span className="flex items-center gap-3">
          <span className="hidden sm:inline">Node size is cost · ring colour is status</span>
          <span>Hover to feel the threads · click a marble for its trace</span>
        </span>
      </figcaption>
    </figure>
  );
}

// ------------------------------------------------------------- hit testing

function pick(
  graph: Graph | null,
  sim: MarbleSim,
  pointer: { x: number; y: number; inside: boolean },
): Hover | null {
  if (!graph || !pointer.inside) return null;

  let best: Body | null = null;
  let bestDistance = Number.POSITIVE_INFINITY;
  for (const body of sim.bodies.values()) {
    const d = Math.hypot(body.x - pointer.x, body.y - pointer.y);
    if (d > body.r * body.scale + 5 || d >= bestDistance) continue;
    best = body;
    bestDistance = d;
  }
  if (best) {
    const node = best.node;
    const m = node.marble;
    return {
      kind: "node",
      id: node.id,
      title: m.summary,
      subtitle: `Step ${node.step} of ${node.stepCount} in ${node.groupLabel} · ${
        m.project_name ?? m.agent_name ?? m.model ?? m.source
      } · ${formatCost(m.cost_usd)} · ${formatRelative(m.occurred_at)}`,
    };
  }

  let bestEdge: { id: string; kind: GraphEdge["kind"]; label: string; distance: number } | null =
    null;
  for (const e of graph.edges) {
    const a = sim.bodies.get(e.source);
    const b = sim.bodies.get(e.target);
    if (!a || !b) continue;
    // Cheap chord test first: the bowed curve never strays further from the
    // chord than its own bow.
    const chord = distanceToSegment(pointer.x, pointer.y, a.x, a.y, b.x, b.y);
    if (chord > HIT_EDGE_TOLERANCE + Math.abs(curvature(e.id)) * 0.12 * Math.hypot(b.x - a.x, b.y - a.y) + 2)
      continue;
    const curve = edgeCurve(e, a, b, null);
    const d = distanceToCurve(pointer.x, pointer.y, a.x, a.y, curve.cx, curve.cy, b.x, b.y);
    if (d > HIT_EDGE_TOLERANCE) continue;
    if (bestEdge && d >= bestEdge.distance) continue;
    bestEdge = { id: e.id, kind: e.kind, label: e.label, distance: d };
  }
  if (!bestEdge) return null;
  return { kind: "edge", id: bestEdge.id, title: EDGE_TITLE[bestEdge.kind], subtitle: bestEdge.label };
}

// ---------------------------------------------------------------- rendering

interface RenderOptions {
  ink: Ink;
  hover: Hover | null;
  pointer: { x: number; y: number; inside: boolean };
  now: number;
  reduceMotion: boolean;
  width: number;
  height: number;
  tooltip: HTMLDivElement | null;
}

function render(
  ctx: CanvasRenderingContext2D,
  graph: Graph | null,
  sim: MarbleSim,
  opts: RenderOptions,
): void {
  const { ink, hover, pointer, now, reduceMotion, tooltip, width: w, height: h } = opts;
  ctx.clearRect(0, 0, w, h);
  if (!graph) {
    if (tooltip) tooltip.style.opacity = "0";
    return;
  }

  let focusEdges: Set<string> | null = null;
  let focusNodes: Set<string> | null = null;
  if (hover?.kind === "node") {
    const incident = sim.neighboursOf(hover.id);
    focusEdges = new Set(incident.map((e) => e.id));
    focusNodes = new Set([hover.id, ...incident.flatMap((e) => [e.source, e.target])]);
  } else if (hover?.kind === "edge") {
    const e = graph.edges.find((x) => x.id === hover.id);
    if (e) {
      focusEdges = new Set([e.id]);
      focusNodes = new Set([e.source, e.target]);
    }
  }

  // 1. links
  for (const e of graph.edges) {
    const a = sim.bodies.get(e.source);
    const b = sim.bodies.get(e.target);
    if (!a || !b) continue;
    const emphasised = focusEdges?.has(e.id) ?? false;
    const dimmed = focusEdges !== null && !emphasised;
    drawEdge(ctx, e, a, b, {
      emphasised,
      dimmed,
      now,
      reduceMotion,
      pointer: pointer.inside ? pointer : null,
      ink,
    });
  }

  // 2. marbles, largest first so small ones stay readable on top
  for (const body of [...sim.bodies.values()].sort((x, y) => y.node.radius - x.node.radius)) {
    const node = body.node;
    const isHovered = hover?.kind === "node" && hover.id === node.id;
    const related = !focusNodes || focusNodes.has(node.id);
    drawMarble(ctx, body, node, { isHovered, alpha: related ? 1 : 0.24, ink, now, reduceMotion });
  }

  // 3. thread names
  drawGroupLabels(ctx, graph, sim, ink, focusNodes);

  // 4. tooltip placement is imperative: the content is React state, the position
  // follows the moving node without another render.
  if (tooltip) positionTooltip(tooltip, graph, sim, hover, w, h);
}

function drawEdge(
  ctx: CanvasRenderingContext2D,
  e: GraphEdge,
  a: Body,
  b: Body,
  opts: {
    emphasised: boolean;
    dimmed: boolean;
    now: number;
    reduceMotion: boolean;
    pointer: { x: number; y: number } | null;
    ink: Ink;
  },
): void {
  const { emphasised, dimmed, now, reduceMotion } = opts;
  const rgb = e.kind === "relay" ? "148,163,184" : hexToRgbString(e.hue);
  const base = e.kind === "relay" ? 0.15 : e.kind === "handoff" ? 0.38 : 0.3;
  const alpha = Math.min(
    (emphasised ? base * 2.4 : dimmed ? base * 0.25 : base) * opts.ink.edge,
    0.95,
  );

  const { cx, cy } = edgeCurve(e, a, b, emphasised && opts.pointer ? opts.pointer : null);

  ctx.save();
  ctx.lineWidth = emphasised ? (e.kind === "relay" ? 1.2 : 1.8) : 1;
  ctx.strokeStyle = `rgba(${rgb},${alpha})`;
  if (e.kind === "handoff") ctx.setLineDash([6, 5]);
  else if (e.kind === "relay") ctx.setLineDash([2, 5]);
  ctx.beginPath();
  ctx.moveTo(a.x, a.y);
  ctx.quadraticCurveTo(cx, cy, b.x, b.y);
  ctx.stroke();
  ctx.setLineDash([]);

  if (emphasised && !reduceMotion && e.kind !== "relay") {
    // A marble rolling down the thread you are pointing at.
    const t = ((now / 1600) + Math.abs(curvature(e.id))) % 1;
    const p = quadPoint(a.x, a.y, cx, cy, b.x, b.y, t);
    const halo = ctx.createRadialGradient(p.x, p.y, 0, p.x, p.y, 8);
    halo.addColorStop(0, `rgba(${rgb},0.9)`);
    halo.addColorStop(1, `rgba(${rgb},0)`);
    ctx.fillStyle = halo;
    ctx.beginPath();
    ctx.arc(p.x, p.y, 8, 0, Math.PI * 2);
    ctx.fill();
  }
  ctx.restore();
}

function drawMarble(
  ctx: CanvasRenderingContext2D,
  body: Body,
  node: GraphNode,
  opts: { isHovered: boolean; alpha: number; ink: Ink; now: number; reduceMotion: boolean },
): void {
  const { isHovered, alpha, ink, now, reduceMotion } = opts;
  const r = node.radius * body.scale * (isHovered ? 1.16 : 1);
  if (r <= 0.5) return;
  const rgb = hexToRgbString(node.color);

  ctx.save();
  ctx.globalAlpha = alpha;

  if (node.glow) {
    const halo = ctx.createRadialGradient(body.x, body.y, r * 0.5, body.x, body.y, r * 2.4);
    halo.addColorStop(0, `rgba(${rgb},0.3)`);
    halo.addColorStop(1, `rgba(${rgb},0)`);
    ctx.fillStyle = halo;
    ctx.beginPath();
    ctx.arc(body.x, body.y, r * 2.4, 0, Math.PI * 2);
    ctx.fill();
  }

  const grad = ctx.createRadialGradient(
    body.x - r * 0.4,
    body.y - r * 0.45,
    r * 0.1,
    body.x,
    body.y,
    r,
  );
  grad.addColorStop(0, tint(node.color, 0.55));
  grad.addColorStop(0.72, node.color);
  grad.addColorStop(1, tint(node.color, -0.34));
  ctx.fillStyle = grad;
  ctx.beginPath();
  ctx.arc(body.x, body.y, r, 0, Math.PI * 2);
  ctx.fill();

  // Specular highlight — what makes it read as glass rather than a dot.
  ctx.fillStyle = "rgba(255,255,255,0.38)";
  ctx.beginPath();
  ctx.arc(body.x - r * 0.36, body.y - r * 0.4, Math.max(r * 0.2, 1), 0, Math.PI * 2);
  ctx.fill();

  const status = STATUS_TINT[node.marble.status];
  if (status) {
    ctx.strokeStyle = `rgba(${status},${isHovered ? 0.95 : 0.5})`;
    ctx.lineWidth = 1.5;
    ctx.beginPath();
    ctx.arc(body.x, body.y, r + 3.5, 0, Math.PI * 2);
    ctx.stroke();
  }

  if (isHovered) {
    ctx.strokeStyle = `rgba(${ink.ring},0.5)`;
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.arc(body.x, body.y, r + 8, 0, Math.PI * 2);
    ctx.stroke();
  }

  // One expanding ring for a marble that has just arrived.
  if (!reduceMotion) {
    const age = now - body.born;
    if (age < 900 && body.scale < 1.01) {
      const t = age / 900;
      ctx.strokeStyle = `rgba(${rgb},${0.5 * (1 - t)})`;
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.arc(body.x, body.y, r + t * 28, 0, Math.PI * 2);
      ctx.stroke();
    }
  }
  ctx.restore();
}

function drawGroupLabels(
  ctx: CanvasRenderingContext2D,
  graph: Graph,
  sim: MarbleSim,
  ink: Ink,
  focusNodes: Set<string> | null,
): void {
  if (graph.nodes.length > 64) return;
  ctx.save();
  ctx.font = "600 10px Inter, ui-sans-serif, system-ui, sans-serif";
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  for (const group of graph.groups) {
    const members = graph.nodes.filter((n) => n.groupKey === group.key);
    if (members.length < 2) continue;
    const bodies = members
      .map((n) => sim.bodies.get(n.id))
      .filter((b): b is Body => b !== undefined);
    if (bodies.length === 0) continue;
    const cx = bodies.reduce((s, b) => s + b.x, 0) / bodies.length;
    const top = Math.min(...members.map((n) => (sim.bodies.get(n.id)?.y ?? 0) - n.radius)) - 16;
    const relevant = !focusNodes || members.some((n) => focusNodes.has(n.id));
    const text = truncate(group.label, 26);
    const half = ctx.measureText(text).width / 2 + 5;
    // A nameplate, so a thread can still be read where marbles crowd it.
    ctx.fillStyle = `rgba(${ink.backing},${relevant ? 0.72 : 0.5})`;
    ctx.beginPath();
    ctx.roundRect?.(cx - half, top - 8, half * 2, 16, 5);
    ctx.fill();
    ctx.fillStyle = `rgba(${relevant ? ink.label : ink.labelDim},${relevant ? 0.75 : 0.35})`;
    ctx.fillText(text, cx, top);
  }
  ctx.restore();
}

function positionTooltip(
  tooltip: HTMLDivElement,
  graph: Graph,
  sim: MarbleSim,
  hover: Hover | null,
  w: number,
  h: number,
): void {
  if (!hover) {
    tooltip.style.opacity = "0";
    return;
  }
  let x: number;
  let y: number;
  if (hover.kind === "node") {
    const body = sim.bodies.get(hover.id);
    if (!body) return;
    x = body.x;
    y = body.y + body.r;
  } else {
    const e = graph.edges.find((k) => k.id === hover.id);
    const a = e && sim.bodies.get(e.source);
    const b = e && sim.bodies.get(e.target);
    if (!e || !a || !b) return;
    const curve = edgeCurve(e, a, b, null);
    const mid = quadPoint(a.x, a.y, curve.cx, curve.cy, b.x, b.y, 0.5);
    x = mid.x;
    y = mid.y;
  }
  tooltip.style.opacity = "1";
  tooltip.style.transform = `translate(${clamp(x - 128, 8, Math.max(w - 264, 8))}px, ${clamp(
    y + 16,
    8,
    Math.max(h - 76, 8),
  )}px)`;
}

// ------------------------------------------------------------------ geometry

interface Curve {
  cx: number;
  cy: number;
}

/**
 * Every link is drawn as a shallow quadratic, bowed deterministically so two
 * threads that share a pair of marbles do not overlap. Hovering makes the
 * curve dip towards the cursor, which is the visual half of the line physics.
 */
function edgeCurve(
  e: GraphEdge,
  a: Body,
  b: Body,
  sag: { x: number; y: number } | null,
): Curve {
  const bow = curvature(e.id) * 0.12;
  let cx = (a.x + b.x) / 2 - (b.y - a.y) * bow;
  let cy = (a.y + b.y) / 2 + (b.x - a.x) * bow;
  if (sag) {
    cx += (sag.x - cx) * 0.3;
    cy += (sag.y - cy) * 0.3;
  }
  return { cx, cy };
}

function distanceToCurve(
  px: number,
  py: number,
  x1: number,
  y1: number,
  cx: number,
  cy: number,
  x2: number,
  y2: number,
): number {
  let closest = Number.POSITIVE_INFINITY;
  for (let i = 0; i <= CURVE_SAMPLES; i++) {
    const t = i / CURVE_SAMPLES;
    const p = quadPoint(x1, y1, cx, cy, x2, y2, t);
    closest = Math.min(closest, Math.hypot(px - p.x, py - p.y));
  }
  return closest;
}

const CURVE_SAMPLES = 10;

function quadPoint(
  x1: number,
  y1: number,
  cx: number,
  cy: number,
  x2: number,
  y2: number,
  t: number,
): { x: number; y: number } {
  const mt = 1 - t;
  return {
    x: mt * mt * x1 + 2 * mt * t * cx + t * t * x2,
    y: mt * mt * y1 + 2 * mt * t * cy + t * t * y2,
  };
}

function distanceToSegment(
  px: number,
  py: number,
  x1: number,
  y1: number,
  x2: number,
  y2: number,
): number {
  const dx = x2 - x1;
  const dy = y2 - y1;
  const len2 = dx * dx + dy * dy;
  if (len2 === 0) return Math.hypot(px - x1, py - y1);
  const t = clamp(((px - x1) * dx + (py - y1) * dy) / len2, 0, 1);
  return Math.hypot(px - (x1 + t * dx), py - (y1 + t * dy));
}

/** Deterministic per-edge curvature in [-1, 1], so parallel links separate. */
function curvature(id: string): number {
  let hash = 0;
  for (let i = 0; i < id.length; i++) hash = (hash * 31 + id.charCodeAt(i)) | 0;
  return ((Math.abs(hash) % 200) - 100) / 100;
}

function tint(hex: string, amount: number): string {
  const [r, g, b] = hexToRgb(hex);
  const target = amount >= 0 ? 255 : 0;
  const t = Math.abs(amount);
  const ch = (c: number) => Math.round(c + (target - c) * t);
  return `rgb(${ch(r)}, ${ch(g)}, ${ch(b)})`;
}

function hexToRgb(hex: string): [number, number, number] {
  const n = parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function hexToRgbString(hex: string): string {
  if (!hex.startsWith("#")) return "148,163,184";
  const [r, g, b] = hexToRgb(hex);
  return `${r},${g},${b}`;
}

function truncate(s: string, max: number): string {
  return s.length > max ? `${s.slice(0, max - 1)}…` : s;
}

function clamp(v: number, min: number, max: number): number {
  return Math.min(Math.max(v, min), max);
}

function Legend({ swatch, label }: { swatch: "solid" | "dashed" | "dotted"; label: string }) {
  const stroke = swatch === "dashed" ? "#fbbf24" : swatch === "dotted" ? "#94a3b8" : "#38bdf8";
  return (
    <span className="flex items-center gap-1.5">
      <svg width="22" height="6" aria-hidden="true" className="shrink-0">
        <line
          x1="1"
          y1="3"
          x2="21"
          y2="3"
          stroke={stroke}
          strokeWidth="1.5"
          strokeDasharray={swatch === "dashed" ? "6 5" : swatch === "dotted" ? "2 4" : undefined}
        />
      </svg>
      {label}
    </span>
  );
}

/** Inviting zero state — an empty constellation should look like a promise. */
export function EmptyGraphState() {
  const dots = [
    { x: 16, y: 34, r: 9, c: colorForKey("objective-a") },
    { x: 34, y: 20, r: 14, c: colorForKey("objective-b") },
    { x: 52, y: 38, r: 7, c: colorForKey("objective-c") },
    { x: 68, y: 22, r: 11, c: colorForKey("objective-d") },
    { x: 84, y: 42, r: 6, c: colorForKey("objective-e") },
  ];
  return (
    <div className="flex h-full flex-col items-center justify-center gap-6 text-center">
      <div className="relative h-16 w-64">
        <svg viewBox="0 0 100 60" className="absolute inset-0 h-full w-full" aria-hidden="true">
          <polyline
            points={dots.map((d) => `${d.x},${d.y}`).join(" ")}
            fill="none"
            stroke="currentColor"
            strokeOpacity="0.22"
            strokeWidth="0.6"
            strokeDasharray="3 3"
          />
        </svg>
        {dots.map((d) => (
          <span
            key={d.c}
            className="absolute rounded-full"
            style={{
              left: `${d.x}%`,
              top: `${d.y}%`,
              width: d.r,
              height: d.r,
              background: `radial-gradient(circle at 32% 30%, ${tint(d.c, 0.5)}, ${d.c} 72%, ${tint(
                d.c,
                -0.3,
              )})`,
              boxShadow: `0 0 18px ${d.c}40`,
            }}
          />
        ))}
      </div>
      <div className="space-y-1.5">
        <p className="text-sm font-medium">No threads yet</p>
        <p className="max-w-xs text-xs text-muted-foreground">
          Finished work lands here and links itself to the objective, project and agent it belongs
          to.
        </p>
      </div>
    </div>
  );
}
