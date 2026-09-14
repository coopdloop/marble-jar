import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
      // The docs markdown lives at the repository root and is inlined via ?raw.
      "@docs": path.resolve(__dirname, "../../docs"),
    },
  },
  server: {
    host: "0.0.0.0",
    port: 5173,
    fs: { allow: [path.resolve(__dirname, "../..")] },
  },
});
