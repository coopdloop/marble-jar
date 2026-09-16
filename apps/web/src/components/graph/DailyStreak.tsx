import { Flame } from "lucide-react";
import type { JarDay } from "@/lib/types";
import { cn, formatCost } from "@/lib/utils";

interface DailyStreakProps {
  daily: JarDay[];
  streakDays: number;
  className?: string;
}

/**
 * The constellation's recent rhythm: a streak badge plus one bar per day.
 * Heights are relative to the busiest day in the window, so the shape is
 * comparable across workspaces of any size.
 */
export function DailyStreak({ daily, streakDays, className }: DailyStreakProps) {
  const max = Math.max(...daily.map((d) => d.marbles), 1);
  const today = daily.length - 1;

  return (
    <div className={cn("flex flex-col items-end gap-1.5", className)}>
      <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Flame
          className={cn("h-3.5 w-3.5", streakDays > 0 ? "text-amber-400" : "text-muted-foreground/50")}
        />
        <span className="numeric font-medium text-foreground">
          {streakDays} day{streakDays === 1 ? "" : "s"}
        </span>
        <span>streak</span>
      </div>

      <div
        className="flex h-8 items-end gap-[3px]"
        role="img"
        aria-label={`Marbles per day for the last ${daily.length} days`}
      >
        {daily.map((d, i) => (
          <div
            key={d.day}
            title={`${d.day}: ${d.marbles} marble${d.marbles === 1 ? "" : "s"} · ${formatCost(d.cost_usd)}`}
            className={cn(
              "w-2 rounded-sm transition-colors",
              d.marbles > 0
                ? cn("bg-primary/70 hover:bg-primary", i === today && "bg-primary ring-1 ring-primary/40")
                : "bg-border/60",
            )}
            style={{ height: d.marbles > 0 ? `${Math.max((d.marbles / max) * 100, 12)}%` : 3 }}
          />
        ))}
      </div>
      <span className="text-[10px] uppercase tracking-wider text-muted-foreground/70">
        last {daily.length} days
      </span>
    </div>
  );
}
