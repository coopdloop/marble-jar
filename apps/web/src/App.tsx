import { useEffect } from "react";
import { Navigate, Route, Routes, useNavigate } from "react-router-dom";
import { AppShell } from "@/components/layout/AppShell";
import { LiveQueuePage } from "@/pages/LiveQueue";
import { ObjectivesPage } from "@/pages/Objectives";
import { ObjectiveDetailPage } from "@/pages/ObjectiveDetail";
import { RulesPage } from "@/pages/Rules";
import { IntegrationsPage } from "@/pages/Integrations";
import { SettingsPage } from "@/pages/Settings";
import { DocsPage } from "@/pages/Docs";
import { LoginPage } from "@/pages/Login";
import { useAuthStore } from "@/stores/useAppStore";

export default function App() {
  const navigate = useNavigate();
  const logout = useAuthStore((s) => s.logout);

  // The API layer emits this when a refresh attempt finally fails.
  useEffect(() => {
    const onUnauthorized = () => {
      logout();
      navigate("/login");
    };
    window.addEventListener("marblejar:unauthorized", onUnauthorized);
    return () => window.removeEventListener("marblejar:unauthorized", onUnauthorized);
  }, [logout, navigate]);

  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route
        element={
          <RequireAuth>
            <AppShell />
          </RequireAuth>
        }
      >
        <Route path="/" element={<LiveQueuePage />} />
        <Route path="/marbles/:marbleId" element={<LiveQueuePage />} />
        <Route path="/objectives" element={<ObjectivesPage />} />
        <Route path="/objectives/:objectiveId" element={<ObjectiveDetailPage />} />
        <Route path="/rules" element={<RulesPage />} />
        <Route path="/integrations" element={<IntegrationsPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="/docs" element={<DocsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

function RequireAuth({ children }: { children: React.ReactNode }) {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  if (!isAuthenticated) return <Navigate to="/login" replace />;
  return <>{children}</>;
}
