import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { CircleDot } from "lucide-react";
import { marbleJarApi } from "@/lib/api";
import { useAuthStore } from "@/stores/useAppStore";
import { Button, Card, CardContent, Input, Label } from "@/components/ui/primitives";

export function LoginPage() {
  const navigate = useNavigate();
  const setSession = useAuthStore((s) => s.setSession);
  const [mode, setMode] = useState<"login" | "register">("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [orgName, setOrgName] = useState("");

  const mutation = useMutation({
    mutationFn: async () => {
      if (mode === "login") {
        const res = await marbleJarApi.login(email, password);
        return { user: res.user, organization: res.organization, tokens: res.tokens };
      }
      const res = await marbleJarApi.register({
        email,
        password,
        organization_name: orgName || undefined,
      });
      return { user: res.user, organization: null, tokens: res.tokens };
    },
    onSuccess: ({ user, organization, tokens }) => {
      setSession(user, organization, tokens);
      navigate("/");
    },
  });

  return (
    <div className="flex min-h-screen items-center justify-center p-4">
      <div className="w-full max-w-sm space-y-6">
        <div className="flex flex-col items-center gap-3 text-center">
          <div className="relative flex h-12 w-12 items-center justify-center rounded-xl bg-gradient-to-br from-primary/25 to-accent/25 ring-1 ring-white/10">
            <div className="absolute inset-0 animate-jar-pulse rounded-xl bg-primary/20 blur-xl" />
            <CircleDot className="relative h-6 w-6 text-primary" />
          </div>
          <div>
            <h1 className="text-xl font-semibold tracking-tight">Marble Jar</h1>
            <p className="mt-1 text-xs text-muted-foreground">
              Every finished task, a marble in the jar.
            </p>
          </div>
        </div>

        <Card>
          <CardContent className="space-y-3 p-5">
            <form
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault();
                mutation.mutate();
              }}
            >
              <div className="space-y-1">
                <Label htmlFor="email">Email</Label>
                <Input
                  id="email"
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="you@company.com"
                  autoComplete="email"
                  required
                />
              </div>

              <div className="space-y-1">
                <Label htmlFor="password">Password</Label>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder={mode === "register" ? "at least 12 characters" : "••••••••"}
                  autoComplete={mode === "login" ? "current-password" : "new-password"}
                  minLength={mode === "register" ? 12 : undefined}
                  required
                />
              </div>

              {mode === "register" ? (
                <div className="space-y-1">
                  <Label htmlFor="org">Workspace name</Label>
                  <Input
                    id="org"
                    value={orgName}
                    onChange={(e) => setOrgName(e.target.value)}
                    placeholder="Acme Engineering"
                  />
                </div>
              ) : null}

              {mutation.isError ? (
                <p className="text-[11px] text-destructive">
                  {(mutation.error as Error).message}
                </p>
              ) : null}

              <Button type="submit" className="w-full" disabled={mutation.isPending}>
                {mutation.isPending
                  ? "Working…"
                  : mode === "login"
                    ? "Sign in"
                    : "Create workspace"}
              </Button>
            </form>

            <button
              type="button"
              onClick={() => setMode(mode === "login" ? "register" : "login")}
              className="w-full text-center text-[11px] text-muted-foreground transition-colors hover:text-foreground"
            >
              {mode === "login"
                ? "No workspace yet? Create one"
                : "Already have an account? Sign in"}
            </button>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
