import { defineConfig } from "vite";
import { HttpProxy } from "vite";
import react from "@vitejs/plugin-react";

const httpTarget: HttpProxy.ProxyTarget = "http://127.0.0.1:1984";

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  base: "",
  server: {
    port: 3000,
    host: true,
    proxy: {
      "/api": {
        target: `${httpTarget}`,
        changeOrigin: true,
        secure: false,
        ws: true,
        // rewrite: (path) => path.replace(/^\/api/, ""),
        cookieDomainRewrite: `${httpTarget}`,
        configure: (proxy, _options) => {
          proxy.on("error", (err, _req, _res) => {
            console.log("proxy error", err);
          });
          proxy.on("proxyReq", (proxyReq, req, _res) => {
            // console.log("Sending Request to the Target:", req.method, req.url)
          });
          proxy.on("proxyRes", (proxyRes, req, _res) => {
            console.log(
              "Received Response from the Target:",
              proxyRes.statusCode,
              req.url
            );
          });
        },
      },
    },
  },
});
