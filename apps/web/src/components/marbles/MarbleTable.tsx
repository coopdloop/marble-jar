import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
  type ColumnDef,
  type SortingState,
} from "@tanstack/react-table";
import { ArrowUpDown, Check, Copy, ExternalLink } from "lucide-react";
import type { Marble } from "@/lib/types";
import { marbleIngestCurl } from "@/lib/curl";
import {
  cn,
  copyToClipboard,
  formatCost,
  formatDuration,
  formatRelative,
  formatTokens,
  marbleColor,
  statusTone,
  totalTokens,
} from "@/lib/utils";
import { Badge, Skeleton } from "@/components/ui/primitives";
import { useUiStore } from "@/stores/useAppStore";

interface MarbleTableProps {
  marbles: Marble[];
  isLoading?: boolean;
  onRowClick?: (id: string) => void;
  draggable?: boolean;
}

export function MarbleTable({ marbles, isLoading, onRowClick, draggable }: MarbleTableProps) {
  const [sorting, setSorting] = useState<SortingState>([]);
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const copyTimer = useRef<number | null>(null);
  const setDragMarble = useUiStore((s) => s.setDragMarble);

  useEffect(() => () => {
    if (copyTimer.current) window.clearTimeout(copyTimer.current);
  }, []);

  const copyIngestCurl = useCallback(async (m: Marble) => {
    if (!(await copyToClipboard(marbleIngestCurl(m)))) return;
    setCopiedId(m.id);
    if (copyTimer.current) window.clearTimeout(copyTimer.current);
    copyTimer.current = window.setTimeout(() => setCopiedId(null), 1800);
  }, []);

  const columns = useMemo<ColumnDef<Marble>[]>(
    () => [
      {
        accessorKey: "summary",
        header: "Work",
        cell: ({ row }) => {
          const m = row.original;
          return (
            <div className="flex min-w-0 items-center gap-2.5">
              <span
                className="h-2.5 w-2.5 shrink-0 rounded-full ring-2 ring-white/10"
                style={{ backgroundColor: marbleColor(m) }}
              />
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">{m.summary}</div>
                <div className="truncate text-[11px] text-muted-foreground">
                  {[m.project_name, m.agent_name, m.source].filter(Boolean).join(" · ")}
                </div>
              </div>
            </div>
          );
        },
      },
      {
        accessorKey: "model",
        header: "Model",
        cell: ({ getValue }) => (
          <span className="text-xs text-muted-foreground">{(getValue() as string) ?? "—"}</span>
        ),
      },
      {
        id: "tokens",
        accessorFn: (m) => totalTokens(m),
        header: "Tokens",
        cell: ({ getValue }) => (
          <span className="numeric text-xs">{formatTokens(getValue() as number)}</span>
        ),
      },
      {
        accessorKey: "cost_usd",
        header: "Cost",
        cell: ({ getValue }) => (
          <span className="numeric text-xs">{formatCost(getValue() as number | null)}</span>
        ),
      },
      {
        accessorKey: "duration_ms",
        header: "Duration",
        cell: ({ getValue }) => (
          <span className="numeric text-xs text-muted-foreground">
            {formatDuration(getValue() as number | null)}
          </span>
        ),
      },
      {
        accessorKey: "status",
        header: "Status",
        cell: ({ getValue }) => {
          const s = getValue() as string;
          return <Badge tone={statusTone(s)}>{s}</Badge>;
        },
      },
      {
        accessorKey: "occurred_at",
        header: "When",
        cell: ({ getValue }) => (
          <span className="whitespace-nowrap text-[11px] text-muted-foreground">
            {formatRelative(getValue() as string)}
          </span>
        ),
      },
      {
        id: "actions",
        header: "",
        cell: ({ row }) => {
          const m = row.original;
          const copied = copiedId === m.id;
          return (
            <div className="flex items-center justify-end gap-2.5">
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  void copyIngestCurl(m);
                }}
                className="text-muted-foreground transition-colors hover:text-primary"
                title={copied ? "Ingest curl copied to clipboard" : "Copy curl that logs this marble"}
              >
                {copied ? (
                  <Check className="h-3.5 w-3.5 text-emerald-400" />
                ) : (
                  <Copy className="h-3.5 w-3.5" />
                )}
              </button>
              {m.phoenix_trace_url ? (
                <a
                  href={m.phoenix_trace_url}
                  target="_blank"
                  rel="noreferrer"
                  onClick={(e) => e.stopPropagation()}
                  className="text-muted-foreground transition-colors hover:text-primary"
                  title="View trace in Phoenix"
                >
                  <ExternalLink className="h-3.5 w-3.5" />
                </a>
              ) : null}
            </div>
          );
        },
      },
    ],
    [copiedId, copyIngestCurl],
  );

  const table = useReactTable({
    data: marbles,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });

  if (isLoading) {
    return (
      <div className="space-y-2 p-4">
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-11 w-full" />
        ))}
      </div>
    );
  }

  if (marbles.length === 0) {
    return (
      <div className="flex h-40 items-center justify-center text-sm text-muted-foreground">
        No marbles match these filters.
      </div>
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-left">
        <thead>
          {table.getHeaderGroups().map((hg) => (
            <tr key={hg.id} className="border-b border-border/60">
              {hg.headers.map((header) => (
                <th
                  key={header.id}
                  className="whitespace-nowrap px-4 py-2.5 text-[11px] font-medium uppercase tracking-wider text-muted-foreground"
                >
                  {header.isPlaceholder ? null : (
                    <button
                      type="button"
                      className={cn(
                        "inline-flex items-center gap-1",
                        header.column.getCanSort() && "transition-colors hover:text-foreground",
                      )}
                      onClick={header.column.getToggleSortingHandler()}
                    >
                      {flexRender(header.column.columnDef.header, header.getContext())}
                      {header.column.getCanSort() && header.column.getIsSorted() ? (
                        <ArrowUpDown className="h-3 w-3" />
                      ) : null}
                    </button>
                  )}
                </th>
              ))}
            </tr>
          ))}
        </thead>
        <tbody>
          {table.getRowModel().rows.map((row) => (
            <tr
              key={row.id}
              draggable={draggable}
              onDragStart={
                draggable
                  ? (e) => {
                      setDragMarble(row.original.id);
                      e.dataTransfer.setData("text/marble-id", row.original.id);
                      e.dataTransfer.effectAllowed = "move";
                    }
                  : undefined
              }
              onDragEnd={draggable ? () => setDragMarble(null) : undefined}
              onClick={() => onRowClick?.(row.original.id)}
              className={cn(
                "border-b border-border/40 transition-colors last:border-0",
                onRowClick && "cursor-pointer hover:bg-secondary/50",
                draggable && "active:opacity-60",
              )}
            >
              {row.getVisibleCells().map((cell) => (
                <td key={cell.id} className="max-w-xs px-4 py-2.5">
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
