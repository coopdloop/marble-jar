import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { CircleDot } from "lucide-react";
import { marbleJarApi } from "@/lib/api";
import { useAuthStore } from "@/stores/useAppStore";
import { Button, Card, CardContent } from "@/components/ui/primitives";

function GoogleMark() {
  return (
    <svg viewBox="0 0 48 48" aria-hidden className="h-4 w-4">
      <path
        fill="#4285F4"
        d="M45.1 24.5c0-1.6-.1-2.8-.4-4H24.4v7.3h11.8c-.2 1.9-1.5 4.8-4.4 6.7l-.1.3 6.4 5 .4.1c4.1-3.8 6.6-9.5 6.6-15.4"
      />
      <path
        fill="#34A853"
        d="M24.4 45.1c5.9 0 10.9-2 14.5-5.3l-6.9-5.4c-1.8 1.3-4.3 2.2-7.6 2.2-5.8 0-10.7-3.8-12.5-9l-.2.1-6.7 5.2-.1.3C8.4 40.2 15.7 45.1 24.4 45.1"
      />
      <path
        fill="#FBBC05"
        d="M11.9 27.6c-.5-1.4-.7-2.9-.7-4.6s.3-3.2.7-4.6l-.1-.4-6.6-5.2-.2.1C3.5 16.1 2.4 19.8 2.4 23s1.1 6.9 2.7 9.9l6.8-5.3"
      />
      <path
        fill="#EA4335"
        d="M24.4 9.1c4.1 0 6.9 1.8 8.5 3.3l6.2-6C35.3 3 30.3 1 24.4 1 15.7 1 8.4 5.9 4.9 13.1l6.9 5.3c1.8-5.5 6.7-9.3 12.6-9.3"
      />
    </svg>
  );
}

export function LoginPage() {
  const navigate = useNavigate();
  const setSession = useAuthStore((s) => s.setSession);
  const [error, setError] = useState<string | null>(null);
  const [googleReady, setGoogleReady] = useState(false);
  const [redeeming, setRedeeming] = useState(false);
  const redeemed = useRef(false);

  // Ask the server what it can actually sign us in with, so a missing client id
  // shows as a hint instead of a dead button.
  useEffect(() => {
    marbleJarApi
      .authConfig()
      .then((cfg) => setGoogleReady(cfg.methods?.google === true))
      .catch(() => setGoogleReady(false));
  }, []);

  // The Google callback hands us a single-use code. It arrives in the URL
  // fragment so it never reaches the web tier's logs or a Referer header.
  useEffect(() => {
    const params = new URLSearchParams(window.location.hash.slice(1));
    const code = params.get("login_code");
    const callbackError = new URLSearchParams(window.location.search).get("error");
    if (callbackError) setError(callbackError);
    if (!code || redeemed.current) return;

    redeemed.current = true;
    setRedeeming(true);
    marbleJarApi
      .exchangeLoginCode(code)
      .then(({ user, organization, tokens }) => {
        setSession(user, organization, tokens);
        navigate("/", { replace: true });
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => {
        setRedeeming(false);
        window.history.replaceState({}, "", window.location.pathname);
      });
  }, [navigate, setSession]);

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
          <CardContent className="space-y-4 p-5">
            {redeeming ? (
              <p className="text-center text-sm text-muted-foreground">
                Finishing sign-in…
              </p>
            ) : (
              <>
                <Button
                  type="button"
                  className="w-full gap-2.5"
                  disabled={!googleReady}
                  onClick={() => {
                    window.location.assign(marbleJarApi.googleSignInUrl());
                  }}
                >
                  <GoogleMark />
                  Sign in with Google
                </Button>

                {!googleReady ? (
                  <p className="text-[11px] text-muted-foreground">
                    Google sign-in is not configured on this server. Set{" "}
                    <code className="mx-1">GOOGLE_OAUTH_CLIENT_ID</code> and{" "}
                    <code>GOOGLE_OAUTH_CLIENT_SECRET</code>, then restart{" "}
                    <code className="mx-1">make dev</code>.
                  </p>
                ) : (
                  <p className="text-[11px] text-muted-foreground">
                    New here? Signing in opens a workspace of your own. To join
                    an existing one, use the link an admin sent you.
                  </p>
                )}
              </>
            )}

            {error ? <p className="text-[11px] text-destructive">{error}</p> : null}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
