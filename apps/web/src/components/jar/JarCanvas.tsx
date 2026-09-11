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

    // Click-to-open: map the clicked point back to a marble id.
    const handleClick = (ev: MouseEvent) => {
      if (!onMarbleClick) return;
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
    render.canvas.addEventListener("click", handleClick);
    render.canvas.style.cursor = "pointer";

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
      render.canvas.removeEventListener("click", handleClick);
      Matter.Render.stop(render);
      Matter.Runner.stop(runner);
      Matter.Composite.clear(engine.world, false);
      Matter.Engine.clear(engine);
      render.canvas.remove();
      bodyMarbleIds.current.clear();
      engineRef.current = null;
      renderRef.current = null;
      runnerRef.current = null;
    };
  }, [onMarbleClick, reduceMotion]);

  // ---- drop a marble into the jar ----
  const dropMarble = (marble: Marble, instant = false) => {
    const engine = engineRef.current;
    if (!engine) return;

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
      render: {
        fillStyle: color,
        strokeStyle: glow ? "rgba(255,255,255,0.9)" : "rgba(255,255,255,0.25)",
        lineWidth: glow ? 2.5 : 1,
      },
    });

    bodyMarbleIds.current.set(body.id, marble.id);
    Matter.Composite.add(engine.world, body);

    // Evict the oldest marbles so the simulation stays bounded.
    const circles = Matter.Composite.allBodies(engine.world).filter((b) => !b.isStatic);
    if (circles.length > MAX_BODIES) {
      const excess = circles.slice(0, circles.length - MAX_BODIES);
      for (const old of excess) {
        bodyMarbleIds.current.delete(old.id);
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

/** Decorative frosted-glass jar silhouette. */
function JarChrome() {
  return (
    <div className="pointer-events-none absolute inset-0 flex items-end justify-center pb-6">
      <div
        className="relative rounded-b-[2.5rem] rounded-t-xl border border-white/15 bg-gradient-to-b from-white/[0.07] to-white/[0.02] backdrop-blur-[2px]"
        style={{ width: "min(55%, 420px)", height: "min(80%, 360px)" }}
      >
        {/* Rim highlight */}
        <div className="absolute inset-x-3 top-0 h-1 rounded-full bg-white/25" />
        {/* Vertical specular highlight */}
        <div className="absolute left-5 top-6 h-2/3 w-8 rounded-full bg-gradient-to-b from-white/20 to-transparent blur-md" />
      </div>
    </div>
  );
}

/** Inviting zero state — an empty jar should look like a promise, not a bug. */
export function EmptyJarState() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 text-center">
      <div className="relative">
        <div className="absolute inset-0 animate-jar-pulse rounded-full bg-primary/20 blur-2xl" />
        <div
          className="relative rounded-b-[2rem] rounded-t-lg border border-white/15 bg-white/[0.04] backdrop-blur-sm"
          style={{ width: 140, height: 170 }}
        >
          <div className="absolute inset-x-3 top-0 h-1 rounded-full bg-white/25" />
        </div>
      </div>
      <div className="space-y-1">
        <p className="text-sm font-medium">Waiting for your first marble</p>
        <p className="max-w-xs text-xs text-muted-foreground">
          Point an agent at the SDK, MCP server or webhook and completed work will start
          dropping in here.
        </p>
      </div>
    </div>
  );
}
