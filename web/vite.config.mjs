import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  base: "/console/",
  build: {
    outDir: "dist/client",
  },
  optimizeDeps: {
    include: ["react", "react-dom/client"],
  },
  server: {
    host: "127.0.0.1",
    proxy: {
      "/v1": process.env.WAVE_WEB_API_URL || "http://127.0.0.1:8080",
      "/health": process.env.WAVE_WEB_API_URL || "http://127.0.0.1:8080",
      "/swagger": process.env.WAVE_WEB_API_URL || "http://127.0.0.1:8080",
    },
    allowedHosts: ["terminal.local"],
    warmup: {
      clientFiles: ["./src/main.jsx"],
    },
  },
  plugins: [react()],
});
