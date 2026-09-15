import { useEffect, useRef, useState } from "react";
import Matter from "matter-js";
import type { Marble } from "@/lib/types";
import { marbleColor, marbleRadius, median } from "@/lib/utils";
import { useUiStore } from "@/stores/useAppStore";

/**
 * The brand moment: marbles physically drop, roll and settle into a glass jar
 * whenever an agent completes work. Bodies carry their marble id so a click on
 * the canvas opens the corresponding detail sheet.
 */

const JAR_WALL = 8;
const MAX_BODIES = 140; // keep the simulation cheap on a wall-mounted TV
const DROP_INTERVAL_MS = 220;

interface JarCanvasProps {
  marbles: Marble[];
  onMarbleClick?: (id: string) => void;
  className?: string;
}

export function JarCanvas({ marbles, onMarbleClick, className }: JarCanvasProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const engineRef = useRef<Matter.Engine | null>(null);
  const renderRef = useRef<Matter.Render | null>(null);
  const runnerRef = useRef<Matter.Runner | null>(null);
  const bodyMarbleIds = useRef<Map<number, string>>(new Map());
  const bodyStyles = useRef<Map<number, MarbleStyle>>(new Map());
  const droppedIds = useRef<Set<string>>(new Set());
  const seeded = useRef(false);

  const [reduceMotion] = useState(
    () => window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false,
  );

  const jarQueue = useUiStore((s) => s.jarQueue);
  const dequeueMarble = useUiStore((s) => s.dequeueMarble);

  // Cost distribution drives marble size/glow; recomputed as data changes.
  const costs = marbles.map((m) => m.cost_usd ?? 0).filter((c) => c > 0);
  const maxCost = costs.length ? Math.max(...costs) : 0;
  const medianCost = median(costs);

  const statsRef = useRef({ maxCost, medianCost });
  statsRef.current = { maxCost, medianCost };

  // ---- engine lifecycle ----
  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const width = container.clientWidth;
    const height = container.clientHeight;

    const engine = Matter.Engine.create({
      gravity: { x: 0, y: reduceMotion ? 0.4 : 1, scale: 0.001 },
    });
    engineRef.current = engine;

    const render = Matter.Render.create({
      element: container,
      engine,
      options: {
        width,
        height,
        background: "transparent",
        wireframes: false,
        pixelRatio: window.devicePixelRatio || 1,
      },
    });
    renderRef.current = render;

    // Jar geometry: a tapered vessel centered in the canvas.
    const jarWidth = Math.min(width * 0.55, 420);
    const jarLeft = (width - jarWidth) / 2;
    const jarBottom = height - 24;
    const jarHeight = Math.min(height * 0.8, 360);
    const jarTop = jarBottom - jarHeight;

    const wallOpts: Matter.IChamferableBodyDefinition = {
      isStatic: true,
      restitution: 0.35,
      friction: 0.6,
      render: { fillStyle: "rgba(255,255,255,0.05)" },
    };

    const floor = Matter.Bodies.rectangle(
      width / 2,
      jarBottom + JAR_WALL / 2,
      jarWidth + JAR_WALL * 2,
      JAR_WALL,
      wallOpts,
    );
    const leftWall = Matter.Bodies.rectangle(
      jarLeft - JAR_WALL / 2,
      jarBottom - jarHeight / 2,
      JAR_WALL,
      jarHeight,
      wallOpts,
    );
    const rightWall = Matter.Bodies.rectangle(
      jarLeft + jarWidth + JAR_WALL / 2,
      jarBottom - jarHeight / 2,
      JAR_WALL,
      jarHeight,
      wallOpts,
    );

    Matter.Composite.add(engine.world, [floor, leftWall, rightWall]);

    // Marbles are drawn as lit spheres (gradient + specular) after each frame.
    const drawStyledMarbles = () => {
      const ctx = render.context;
      for (const body of Matter.Composite.allBodies(engine.world)) {
        if (body.isStatic) continue;
        const style = bodyStyles.current.get(body.id);
        if (!style) continue;
        drawMarble(ctx, body.position.x, body.position.y, style.radius, style.color, style.glow);
      }
    };
    Matter.Events.on(render, "afterRender", drawStyledMarbles);

    const runner = Matter.Runner.create();
    runnerRef.current = runner;
    Matter.Runner.run(runner, engine);
    Matter.Render.run(render);

    // Store jar geometry for the drop routine.
    (engine as unknown as { jarGeom: JarGeom }).jarGeom = {
      jarLeft,
      jarWidth,
      jarTop,
      jarBottom,
    };

    // Click-and-hold drag: marbles are grabbable, flingable, just for fun.
    const mouse = Matter.Mouse.create(render.canvas);
    const mouseConstraint = Matter.MouseConstraint.create(engine, {
      mouse,
      constraint: { stiffness: 0.2, damping: 0.1, render: { visible: false } },
    });
    Matter.Composite.add(engine.world, mouseConstraint);

    const handleStartDrag = (ev: unknown) => {
      const body = (ev as { body?: Matter.Body }).body;
      // Only marbles are grabbable — never the jar walls.
      if (!body || !bodyMarbleIds.current.has(body.id)) {
        mouseConstraint.constraint.bodyB = null;
        return;
      }
      render.canvas.style.cursor = "grabbing";
    };
    const handleEndDrag = () => {
      render.canvas.style.cursor = "grab";
    };
    Matter.Events.on(mouseConstraint, "startdrag", handleStartDrag);
    Matter.Events.on(mouseConstraint, "enddrag", handleEndDrag);

    // Click-to-open: map the clicked point back to a marble id. A click that
    // travelled is a drag release, not an intent to open the detail sheet.
    let downPos: { x: number; y: number } | null = null;
    const handleDown = (ev: MouseEvent) => {
      downPos = { x: ev.clientX, y: ev.clientY };
    };
    const handleClick = (ev: MouseEvent) => {
      if (!onMarbleClick) return;
      if (downPos && Math.hypot(ev.clientX - downPos.x, ev.clientY - downPos.y) > 6) return;
      const rect = render.canvas.getBoundingClientRect();
      const point = { x: ev.clientX - rect.left, y: ev.clientY - rect.top };
      const hits = Matter.Query.point(Matter.Composite.allBodies(engine.world), point);
      for (const body of hits) {
        const id = bodyMarbleIds.current.get(body.id);
        if (id) {
          onMarbleClick(id);
          return;
        }
      }
    };
    render.canvas.addEventListener("mousedown", handleDown);
    render.canvas.addEventListener("click", handleClick);
    render.canvas.style.cursor = "grab";

    // Keep the canvas crisp and the jar centered on resize.
    const resizeObserver = new ResizeObserver(() => {
      const w = container.clientWidth;
      const h = container.clientHeight;
      render.canvas.width = w * (window.devicePixelRatio || 1);
      render.canvas.height = h * (window.devicePixelRatio || 1);
      render.canvas.style.width = `${w}px`;
      render.canvas.style.height = `${h}px`;
      render.options.width = w;
      render.options.height = h;
    });
    resizeObserver.observe(container);

    return () => {
      resizeObserver.disconnect();
      render.canvas.removeEventListener("mousedown", handleDown);
      render.canvas.removeEventListener("click", handleClick);
      Matter.Events.off(mouseConstraint, "startdrag", handleStartDrag);
      Matter.Events.off(mouseConstraint, "enddrag", handleEndDrag);
      Matter.Mouse.clearSourceEvents(mouse);
      Matter.Events.off(render, "afterRender", drawStyledMarbles);
      Matter.Render.stop(render);
      Matter.Runner.stop(runner);
      Matter.Composite.clear(engine.world, false);
      Matter.Engine.clear(engine);
      render.canvas.remove();
      bodyMarbleIds.current.clear();
      bodyStyles.current.clear();
      droppedIds.current.clear();
      engineRef.current = null;
      renderRef.current = null;
      runnerRef.current = null;
    };
  }, [onMarbleClick, reduceMotion]);

  // ---- drop a marble into the jar ----
  const dropMarble = (marble: Marble, instant = false) => {
    const engine = engineRef.current;
    if (!engine) return;

    // One marble, one physical body — ever.
    if (droppedIds.current.has(marble.id)) return;
    droppedIds.current.add(marble.id);

    const geom = (engine as unknown as { jarGeom?: JarGeom }).jarGeom;
    if (!geom) return;

    const { maxCost: mx, medianCost: md } = statsRef.current;
    const radius = marbleRadius(marble, mx || marble.cost_usd || 1);
    const color = marbleColor(marble);
    const glow = md > 0 && (marble.cost_usd ?? 0) > md * 3;

    // Random x inside the jar mouth so the pile looks organic.
    const pad = radius + JAR_WALL + 4;
    const x = geom.jarLeft + pad + Math.random() * Math.max(geom.jarWidth - pad * 2, 1);
    const y = instant
      ? geom.jarBottom - 40 - Math.random() * (geom.jarBottom - geom.jarTop - 60)
      : geom.jarTop - 40;

    const body = Matter.Bodies.circle(x, y, radius, {
      restitution: 0.45,
      friction: 0.12,
      frictionAir: 0.008,
      density: 0.0014,
      render: { fillStyle: color },
    });

    bodyMarbleIds.current.set(body.id, marble.id);
    bodyStyles.current.set(body.id, { color, glow, radius });
    Matter.Composite.add(engine.world, body);

    if (import.meta.env.DEV) {
      // Dev-only instrumentation used by UI tests to assert exactly-once drops.
      const w = window as unknown as { __mjDropped?: number };
      w.__mjDropped = (w.__mjDropped ?? 0) + 1;
    }

    // Evict the oldest marbles so the simulation stays bounded.
    const circles = Matter.Composite.allBodies(engine.world).filter((b) => !b.isStatic);
    if (circles.length > MAX_BODIES) {
      const excess = circles.slice(0, circles.length - MAX_BODIES);
      for (const old of excess) {
        bodyMarbleIds.current.delete(old.id);
        bodyStyles.current.delete(old.id);
        Matter.Composite.remove(engine.world, old);
      }
    }
  };

  // ---- seed the jar with existing marbles (once) ----
  useEffect(() => {
    if (seeded.current || marbles.length === 0 || !engineRef.current) return;
    seeded.current = true;

    // Oldest first so recent work sits visibly on top.
    const seedSet = marbles.slice(0, 60).reverse();
    seedSet.forEach((m, i) => {
      setTimeout(() => dropMarble(m, true), i * 18);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [marbles.length]);

  // ---- drain the live event queue, sequencing drops smoothly ----
  useEffect(() => {
    if (jarQueue.length === 0) return;

    const timer = setInterval(() => {
      const next = dequeueMarble();
      if (next) dropMarble(next);
    }, DROP_INTERVAL_MS);

    return () => clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jarQueue.length, dequeueMarble]);

  return (
    <div className={className}>
      <div className="relative h-full w-full">
        {/* Glass jar chrome drawn behind the physics canvas. */}
        <JarChrome />
        <div ref={containerRef} className="absolute inset-0 [&>canvas]:!block" />
      </div>
    </div>
  );
}

interface JarGeom {
  jarLeft: number;
  jarWidth: number;
  jarTop: number;
  jarBottom: number;
}

interface MarbleStyle {
  color: string;
  glow: boolean;
  radius: number;
}

function hexToRgb(hex: string): [number, number, number] {
  const n = parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

/** amount >= 0 lightens toward white, < 0 darkens toward black. */
function tint(hex: string, amount: number): string {
  const [r, g, b] = hexToRgb(hex);
  const target = amount >= 0 ? 255 : 0;
  const t = Math.abs(amount);
  const ch = (c: number) => Math.round(c + (target - c) * t);
  return `rgb(${ch(r)}, ${ch(g)}, ${ch(b)})`;
}

function rgba(hex: string, alpha: number): string {
  const [r, g, b] = hexToRgb(hex);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

function drawMarble(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  r: number,
  color: string,
  glow: boolean,
) {
  ctx.save();

  if (glow) {
    const halo = ctx.createRadialGradient(x, y, r * 0.5, x, y, r * 2.4);
    halo.addColorStop(0, rgba(color, 0.35));
    halo.addColorStop(1, rgba(color, 0));
    ctx.fillStyle = halo;
    ctx.beginPath();
    ctx.arc(x, y, r * 2.4, 0, Math.PI * 2);
    ctx.fill();
  }

  const grad = ctx.createRadialGradient(x - r * 0.4, y - r * 0.45, r * 0.1, x, y, r);
  grad.addColorStop(0, tint(color, 0.5));
  grad.addColorStop(0.75, color);
  grad.addColorStop(1, tint(color, -0.3));
  ctx.fillStyle = grad;
  ctx.beginPath();
  ctx.arc(x, y, r, 0, Math.PI * 2);
  ctx.fill();

  ctx.fillStyle = "rgba(255,255,255,0.35)";
  ctx.beginPath();
  ctx.arc(x - r * 0.38, y - r * 0.42, r * 0.22, 0, Math.PI * 2);
  ctx.fill();

  ctx.restore();
}

/** Minimal hairline jar silhouette — just enough to read as a vessel. */
function JarChrome() {
  return (
    <div className="pointer-events-none absolute inset-0 flex items-end justify-center pb-6">
      <div
        className="relative overflow-hidden rounded-b-[3rem] rounded-t-lg border border-white/10"
        style={{
          width: "min(55%, 420px)",
          height: "min(80%, 360px)",
          background: "linear-gradient(180deg, rgba(255,255,255,0.03), transparent 40%)",
        }}
      >
        <div className="absolute inset-x-2 top-px h-px bg-white/15" />
        {/* Grounding shadow so the pile sits on glass, not on void. */}
        <div className="absolute inset-x-0 bottom-0 h-20 bg-gradient-to-b from-transparent to-black/25" />
      </div>
    </div>
  );
}

/** Inviting zero state — an empty jar should look like a promise, not a bug. */
export function EmptyJarState() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-5 text-center">
      <div className="relative">
        <div className="absolute inset-0 animate-jar-pulse rounded-full bg-primary/10 blur-2xl" />
        <div
          className="relative rounded-b-[2.25rem] rounded-t-md border border-white/10 bg-white/[0.02]"
          style={{ width: 140, height: 180 }}
        >
          <div className="absolute inset-x-2 top-px h-px bg-white/15" />
        </div>
      </div>
      <div className="space-y-1.5">
        <p className="text-sm font-medium">Waiting for your first marble</p>
        <p className="max-w-xs text-xs text-muted-foreground">
          Completed work drops in here as soon as your agents report back.
        </p>
      </div>
    </div>
  );
}
