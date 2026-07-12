import { useEffect, useMemo, useRef, useState } from "react";
import {
  forceCenter,
  forceCollide,
  forceLink,
  forceManyBody,
  forceSimulation,
  type Simulation,
} from "d3-force";
import { api, type GraphData, type QueryTrace, type TraceSummary } from "../api";

interface SimNode {
  path: string;
  title?: string;
  type?: string;
  links: number;
  x: number;
  y: number;
  fx?: number | null;
  fy?: number | null;
}

interface SimLink {
  source: SimNode;
  target: SimNode;
}

// Channel palette, assigned to types in first-seen order.
const PALETTE = ["#64c8ff", "#a78bfa", "#34d399", "#fbbf24", "#f87171", "#f472b6", "#2dd4bf", "#a3e635"];

const KIND_COLOR: Record<TraceSummary["kind"], string> = {
  query: "#64c8ff",
  chat: "#34d399",
  mutation: "#fbbf24",
};

const radius = (n: SimNode) => 5 + Math.sqrt(n.links) * 3.5;

/** The traversal chain: concept visits in step order (reads + writes), deduped consecutively. */
function traceVisits(trace: QueryTrace): { path: string; seq: number; write: boolean }[] {
  const visits: { path: string; seq: number; write: boolean }[] = [];
  for (const step of trace.steps) {
    if (step.tool === "read_concept" || step.write) {
      const p = step.paths[0];
      if (p && visits[visits.length - 1]?.path !== p) {
        visits.push({ path: p, seq: step.seq, write: !!step.write });
      }
    }
  }
  return visits;
}

/**
 * Obsidian-style force-directed view of the memory graph, plus query-path
 * replay: pick a recorded agent run and its traversal is drawn over the
 * graph as numbered directed hops.
 */
export function GraphView({
  refreshKey,
  onNavigate,
}: {
  refreshKey: number;
  onNavigate: (path: string) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const simRef = useRef<Simulation<SimNode, SimLink> | null>(null);
  const [nodes, setNodes] = useState<SimNode[]>([]);
  const [links, setLinks] = useState<SimLink[]>([]);
  const [tick, setTick] = useState(0);
  const [hovered, setHovered] = useState<string | null>(null);
  const [view, setView] = useState({ x: 0, y: 0, k: 1 });
  const [traces, setTraces] = useState<TraceSummary[]>([]);
  const [pathsOpen, setPathsOpen] = useState(true);
  const [activeTrace, setActiveTrace] = useState<QueryTrace | null>(null);
  const dragRef = useRef<{ mode: "node" | "pan"; node?: SimNode; lastX: number; lastY: number } | null>(null);

  // Build / rebuild the simulation when data changes.
  useEffect(() => {
    let cancelled = false;
    api.graph().then((graph: GraphData) => {
      if (cancelled) return;
      const width = containerRef.current?.clientWidth ?? 800;
      const height = containerRef.current?.clientHeight ?? 600;
      const simNodes: SimNode[] = graph.nodes.map((n, i) => ({
        path: n.path,
        title: n.title,
        type: n.type,
        links: n.links,
        x: width / 2 + 120 * Math.cos((2 * Math.PI * i) / Math.max(1, graph.nodes.length)),
        y: height / 2 + 120 * Math.sin((2 * Math.PI * i) / Math.max(1, graph.nodes.length)),
      }));
      const byPath = new Map(simNodes.map((n) => [n.path, n]));
      const simLinks: SimLink[] = graph.edges
        .filter((e) => byPath.has(e.source) && byPath.has(e.target))
        .map((e) => ({ source: byPath.get(e.source)!, target: byPath.get(e.target)! }));

      simRef.current?.stop();
      const sim = forceSimulation<SimNode>(simNodes)
        .force("charge", forceManyBody().strength(-220))
        .force("link", forceLink<SimNode, SimLink>(simLinks).distance(90).strength(0.6))
        .force("center", forceCenter(width / 2, height / 2))
        .force("collide", forceCollide<SimNode>((n) => radius(n) + 6))
        .on("tick", () => setTick((t) => t + 1));
      simRef.current = sim;
      setNodes(simNodes);
      setLinks(simLinks);
      setView({ x: 0, y: 0, k: 1 });
    });
    api.traces().then(setTraces).catch(() => {});
    return () => {
      cancelled = true;
      simRef.current?.stop();
    };
  }, [refreshKey]);

  const typeColors = useMemo(() => {
    const map = new Map<string, string>();
    for (const n of nodes) {
      const t = n.type ?? "unknown";
      if (!map.has(t)) map.set(t, PALETTE[map.size % PALETTE.length]);
    }
    return map;
  }, [nodes]);

  const neighbors = useMemo(() => {
    if (!hovered) return null;
    const set = new Set<string>([hovered]);
    for (const l of links) {
      if (l.source.path === hovered) set.add(l.target.path);
      if (l.target.path === hovered) set.add(l.source.path);
    }
    return set;
  }, [hovered, links]);

  const nodeByPath = useMemo(() => new Map(nodes.map((n) => [n.path, n])), [nodes]);

  // Path-replay derived state.
  const visits = useMemo(
    () => (activeTrace ? traceVisits(activeTrace).filter((v) => nodeByPath.has(v.path)) : []),
    [activeTrace, nodeByPath]
  );
  const pathSet = useMemo(() => new Set(visits.map((v) => v.path)), [visits]);
  const searchHitSet = useMemo(() => {
    if (!activeTrace) return new Set<string>();
    return new Set(
      activeTrace.steps.filter((s) => s.tool === "search_knowledge").flatMap((s) => s.paths)
    );
  }, [activeTrace]);

  const selectTrace = async (id: string) => {
    const full = await api.trace(id);
    setActiveTrace(full);
  };

  // ── Interaction ──────────────────────────────────────────────────────

  const toWorld = (clientX: number, clientY: number) => {
    const rect = containerRef.current!.getBoundingClientRect();
    return {
      x: (clientX - rect.left - view.x) / view.k,
      y: (clientY - rect.top - view.y) / view.k,
    };
  };

  const onPointerDown = (e: React.PointerEvent, node?: SimNode) => {
    e.preventDefault();
    (e.target as Element).setPointerCapture?.(e.pointerId);
    dragRef.current = { mode: node ? "node" : "pan", node, lastX: e.clientX, lastY: e.clientY };
    if (node) {
      node.fx = node.x;
      node.fy = node.y;
      simRef.current?.alphaTarget(0.3).restart();
    }
  };

  const onPointerMove = (e: React.PointerEvent) => {
    const drag = dragRef.current;
    if (!drag) return;
    if (drag.mode === "node" && drag.node) {
      const w = toWorld(e.clientX, e.clientY);
      drag.node.fx = w.x;
      drag.node.fy = w.y;
    } else {
      setView((v) => ({ ...v, x: v.x + e.clientX - drag.lastX, y: v.y + e.clientY - drag.lastY }));
      drag.lastX = e.clientX;
      drag.lastY = e.clientY;
    }
  };

  const onPointerUp = () => {
    const drag = dragRef.current;
    if (drag?.node) {
      drag.node.fx = null;
      drag.node.fy = null;
      simRef.current?.alphaTarget(0);
    }
    dragRef.current = null;
  };

  const onWheel = (e: React.WheelEvent) => {
    const rect = containerRef.current!.getBoundingClientRect();
    const mx = e.clientX - rect.left;
    const my = e.clientY - rect.top;
    setView((v) => {
      const k = Math.min(4, Math.max(0.25, v.k * (e.deltaY < 0 ? 1.15 : 1 / 1.15)));
      return { k, x: mx - ((mx - v.x) / v.k) * k, y: my - ((my - v.y) / v.k) * k };
    });
  };

  const showAllLabels = nodes.length <= 60;
  const pathColor = activeTrace ? KIND_COLOR[activeTrace.kind] : "#64c8ff";
  void tick;

  const dimmedBy = (path: string): boolean => {
    if (activeTrace) return !pathSet.has(path) && !searchHitSet.has(path);
    if (neighbors) return !neighbors.has(path);
    return false;
  };

  return (
    <div ref={containerRef} className="relative h-full w-full overflow-hidden bg-zinc-950">
      {/* Legend */}
      <div className="absolute left-3 top-3 z-10 space-y-1 rounded-lg border border-zinc-800 bg-zinc-900/80 p-2 text-xs">
        {[...typeColors.entries()].map(([t, c]) => (
          <div key={t} className="flex items-center gap-2">
            <span className="h-2.5 w-2.5 rounded-full" style={{ background: c }} />
            <span className="text-zinc-300">{t}</span>
          </div>
        ))}
        {nodes.some((n) => n.links === 0) && (
          <div className="flex items-center gap-2 border-t border-zinc-800 pt-1">
            <span className="h-2.5 w-2.5 rounded-full border border-red-500" />
            <span className="text-zinc-400">orphan (unlinked)</span>
          </div>
        )}
      </div>

      {/* Query paths panel */}
      <div className="absolute right-3 top-3 z-10 w-72 rounded-lg border border-zinc-800 bg-zinc-900/90 text-xs">
        <button
          onClick={() => setPathsOpen(!pathsOpen)}
          className="flex w-full items-center px-3 py-2 font-semibold text-zinc-300 hover:text-zinc-100"
        >
          Query paths
          <span className="ml-auto text-zinc-500">{pathsOpen ? "▾" : "▸"}</span>
        </button>
        {pathsOpen && (
          <div className="max-h-72 space-y-1 overflow-y-auto border-t border-zinc-800 p-2">
            {traces.length === 0 && (
              <p className="p-2 text-zinc-500">
                No recorded runs yet — ask the agent something and its traversal will appear here.
              </p>
            )}
            {traces.map((t) => (
              <button
                key={t.id}
                onClick={() => (activeTrace?.id === t.id ? setActiveTrace(null) : selectTrace(t.id))}
                className={`block w-full rounded px-2 py-1.5 text-left hover:bg-zinc-800 ${
                  activeTrace?.id === t.id ? "bg-zinc-800 ring-1 ring-zinc-700" : ""
                }`}
              >
                <div className="flex items-center gap-1.5">
                  <span
                    className="h-2 w-2 shrink-0 rounded-full"
                    style={{ background: KIND_COLOR[t.kind] }}
                    title={t.kind}
                  />
                  <span className="truncate text-zinc-200">{t.input}</span>
                </div>
                <div className="mt-0.5 truncate font-mono text-[10px] text-zinc-500">{t.notation}</div>
              </button>
            ))}
          </div>
        )}
      </div>

      {/* Active path strip */}
      {activeTrace && (
        <div className="absolute bottom-3 left-1/2 z-10 w-[min(90%,800px)] -translate-x-1/2 rounded-lg border border-zinc-800 bg-zinc-900/95 px-3 py-2">
          <div className="flex items-center gap-2 text-xs">
            <span
              className="rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase"
              style={{ background: `${pathColor}22`, color: pathColor }}
            >
              {activeTrace.kind}
            </span>
            <span className="truncate text-zinc-200">{activeTrace.input}</span>
            <button
              onClick={() => setActiveTrace(null)}
              className="ml-auto shrink-0 rounded px-1.5 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100"
            >
              ✕
            </button>
          </div>
          <div className="mt-1 overflow-x-auto whitespace-nowrap font-mono text-[11px] text-zinc-400">
            {activeTrace.notation}
          </div>
        </div>
      )}

      {!activeTrace && (
        <div className="absolute bottom-3 left-3 z-10 text-[11px] text-zinc-600">
          {nodes.length} concepts · {links.length} links — drag nodes · scroll to zoom · click to open
        </div>
      )}

      <svg
        className="h-full w-full cursor-grab active:cursor-grabbing"
        onPointerDown={(e) => onPointerDown(e)}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onWheel={onWheel}
      >
        <defs>
          <marker id="path-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M 0 1 L 9 5 L 0 9 z" fill={pathColor} />
          </marker>
        </defs>
        <g transform={`translate(${view.x},${view.y}) scale(${view.k})`}>
          {links.map((l, i) => {
            const dim = activeTrace
              ? !(pathSet.has(l.source.path) && pathSet.has(l.target.path))
              : neighbors && !(neighbors.has(l.source.path) && neighbors.has(l.target.path));
            return (
              <line
                key={i}
                x1={l.source.x}
                y1={l.source.y}
                x2={l.target.x}
                y2={l.target.y}
                stroke="#3f3f46"
                strokeWidth={1.2 / view.k}
                opacity={dim ? 0.1 : 0.7}
              />
            );
          })}

          {/* Traversal overlay: numbered directed hops */}
          {visits.slice(0, -1).map((v, i) => {
            const a = nodeByPath.get(v.path)!;
            const b = nodeByPath.get(visits[i + 1].path)!;
            const mx = (a.x + b.x) / 2;
            const my = (a.y + b.y) / 2;
            // Perpendicular bow so repeated hops between the same pair stay readable.
            const dx = b.x - a.x;
            const dy = b.y - a.y;
            const len = Math.max(1, Math.hypot(dx, dy));
            const off = 22 * (i % 2 === 0 ? 1 : -1);
            const cx = mx + (-dy / len) * off;
            const cy = my + (dx / len) * off;
            return (
              <g key={`hop-${i}`} style={{ pointerEvents: "none" }}>
                <path
                  d={`M ${a.x} ${a.y} Q ${cx} ${cy} ${b.x} ${b.y}`}
                  fill="none"
                  stroke={pathColor}
                  strokeWidth={2 / view.k}
                  strokeDasharray={`${6 / view.k} ${4 / view.k}`}
                  markerEnd="url(#path-arrow)"
                  opacity={0.9}
                />
                <circle cx={cx} cy={cy} r={9 / view.k} fill="#18181b" stroke={pathColor} strokeWidth={1.5 / view.k} />
                <text x={cx} y={cy + 3.5 / view.k} textAnchor="middle" fill={pathColor} fontSize={10 / view.k} fontWeight={700}>
                  {i + 1}
                </text>
              </g>
            );
          })}

          {nodes.map((n) => {
            const color = typeColors.get(n.type ?? "unknown") ?? "#64c8ff";
            const dim = dimmedBy(n.path);
            const onPath = pathSet.has(n.path);
            const isSearchHit = activeTrace != null && !onPath && searchHitSet.has(n.path);
            const r = radius(n);
            return (
              <g
                key={n.path}
                transform={`translate(${n.x},${n.y})`}
                opacity={dim ? 0.15 : 1}
                className="cursor-pointer"
                onPointerDown={(e) => {
                  e.stopPropagation();
                  onPointerDown(e, n);
                }}
                onPointerEnter={() => setHovered(n.path)}
                onPointerLeave={() => setHovered(null)}
                onClick={() => onNavigate(n.path)}
              >
                {n.links === 0 && (
                  <circle r={r + 3} fill="none" stroke="#ef4444" strokeWidth={1.5 / view.k} opacity={0.8} />
                )}
                {onPath && (
                  <circle r={r + 5} fill="none" stroke={pathColor} strokeWidth={2 / view.k} opacity={0.9} />
                )}
                {isSearchHit && (
                  <circle r={r + 4} fill="none" stroke={pathColor} strokeWidth={1 / view.k} strokeDasharray={`${3 / view.k} ${3 / view.k}`} opacity={0.5} />
                )}
                <circle r={r + 5} fill={color} opacity={hovered === n.path ? 0.25 : 0} />
                <circle r={r} fill={color} stroke="#18181b" strokeWidth={1.5} />
                {(showAllLabels || hovered === n.path || onPath || (neighbors?.has(n.path) ?? false)) && (
                  <text
                    y={r + 14 / view.k}
                    textAnchor="middle"
                    fill="#d4d4d8"
                    fontSize={12 / view.k}
                    style={{ pointerEvents: "none", userSelect: "none" }}
                  >
                    {n.title ?? n.path.split("/").pop()}
                  </text>
                )}
              </g>
            );
          })}
        </g>
      </svg>
    </div>
  );
}
