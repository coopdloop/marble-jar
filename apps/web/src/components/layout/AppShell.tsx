import { NavLink, Outlet, useNavigate } from "react-router-dom";
import {
  Activity,
  BookOpen,
  CircleDot,
  LogOut,
  Moon,
  Plug,
  Settings as SettingsIcon,
  Sun,
  Target,
  Workflow,
} from "lucide-react";
import { useQueueFeed } from "@/hooks/useQueueFeed";
import { useAuthStore, useUiStore } from "@/stores/useAppStore";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/primitives";
import type { ConnectionStatus } from "@/lib/types";

const NAV = [
  { to: "/", label: "Queue", icon: Activity, end: true },
  { to: "/objectives", label: "Objectives", icon: Target },
  { to: "/rules", label: "Rules", icon: Workflow },
  { to: "/integrations", label: "Integrations", icon: Plug },
  { to: "/docs", label: "Docs", icon: BookOpen },
  { to: "/settings", label: "Settings", icon: SettingsIcon },
];

export function AppShell() {
  // A single socket for the whole app, mounted at the shell.
  useQueueFeed();

  const navigate = useNavigate();
  const { user, organization, logout } = useAuthStore();
  const { theme, toggleTheme, wsStatus } = useUiStore();

  return (
    <div className="flex min-h-screen">
      <aside className="sticky top-0 hidden h-screen w-56 shrink-0 flex-col border-r border-border/60 bg-card/40 backdrop-blur-xl md:flex">
        <div className="flex items-center gap-2.5 px-5 py-5">
          <JarMark />
          <div className="leading-tight">
            <div className="text-sm font-semibold tracking-tight">Marble Jar</div>
            <div className="text-[11px] text-muted-foreground">
              {organization?.name ?? "workspace"}
            </div>
          </div>
        </div>

        <nav className="flex flex-1 flex-col gap-0.5 px-3">
          {NAV.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors",
                  isActive
                    ? "bg-primary/10 font-medium text-primary"
                    : "text-muted-foreground hover:bg-secondary hover:text-foreground",
                )
              }
            >
              <Icon className="h-4 w-4" />
              {label}
            </NavLink>
          ))}
        </nav>

        <div className="space-y-2 border-t border-border/60 p-3">
          <div className="px-2 text-[11px] text-muted-foreground">
            {user?.display_name ?? user?.email ?? "signed in"}
          </div>
          <div className="flex gap-1.5">
            <Button variant="ghost" size="sm" className="flex-1" onClick={toggleTheme}>
              {theme === "dark" ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
              Theme
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                logout();
                navigate("/login");
              }}
              aria-label="Sign out"
            >
              <LogOut className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-20 flex h-14 items-center justify-between gap-4 border-b border-border/60 bg-background/70 px-5 backdrop-blur-xl">
          <div className="flex items-center gap-2 md:hidden">
            <JarMark />
            <span className="text-sm font-semibold">Marble Jar</span>
          </div>
          <div className="ml-auto flex items-center gap-3">
            <ConnectionStatusPill status={wsStatus} />
            <div className="hidden items-center gap-2 rounded-full border border-border/60 bg-card/60 px-3 py-1 text-[11px] text-muted-foreground sm:flex">
              <span className="h-1.5 w-1.5 rounded-full bg-accent" />
              acting as {user?.email ?? "—"}
            </div>
          </div>
        </header>

        <main className="min-w-0 flex-1 p-5">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

export function ConnectionStatusPill({ status }: { status: ConnectionStatus }) {
  const config: Record<ConnectionStatus, { label: string; dot: string; text: string }> = {
    connected: {
      label: "Live",
      dot: "bg-[hsl(var(--success))]",
      text: "text-[hsl(var(--success))]",
    },
    connecting: { label: "Connecting", dot: "bg-[hsl(var(--warning))]", text: "text-[hsl(var(--warning))]" },
    reconnecting: {
      label: "Reconnecting",
      dot: "bg-[hsl(var(--warning))]",
      text: "text-[hsl(var(--warning))]",
    },
    polling: { label: "Polling", dot: "bg-muted-foreground", text: "text-muted-foreground" },
  };
  const c = config[status];

  return (
    <div className="flex items-center gap-1.5 rounded-full border border-border/60 bg-card/60 px-2.5 py-1">
      <span className={cn("h-1.5 w-1.5 rounded-full", c.dot, status === "connected" && "animate-pill-pulse")} />
      <span className={cn("text-[11px] font-medium", c.text)}>{c.label}</span>
    </div>
  );
}

function JarMark() {
  return (
    <div className="relative flex h-7 w-7 items-center justify-center rounded-lg bg-gradient-to-br from-primary/25 to-accent/25 ring-1 ring-white/10">
      <CircleDot className="h-4 w-4 text-primary" />
    </div>
  );
}
