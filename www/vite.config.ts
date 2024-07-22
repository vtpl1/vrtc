import { defineConfig } from "vite";
import { HttpProxy, splitVendorChunkPlugin } from "vite";
import react from "@vitejs/plugin-react";

// const webRtcTarget: HttpProxy.ProxyTarget = "http://65.2.118.177:8083"; // "https://meraface-1m.videonetics.com:9443"
const webRtcTarget: HttpProxy.ProxyTarget = "http://172.16.1.138:8083"; // "https://meraface-1m.videonetics.com:9443"
const httpTarget: HttpProxy.ProxyTarget = "http://127.0.0.1:58000";

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react(), splitVendorChunkPlugin()],
  base: "",
  server: {
    port: 3000,
    host: true,
    proxy: {
      "/api/stream": {
        target: `${webRtcTarget}`,
        changeOrigin: true,
        secure: false,
        ws: true,
        rewrite: (path) => path.replace(/^\/api/, ""),
        cookieDomainRewrite: `${webRtcTarget}`,
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
      "/api/v2": {
        target: `${webRtcTarget}`,
        changeOrigin: true,
        secure: false,
        ws: true,
        rewrite: (path) => path.replace(/^\/api/, ""),
        cookieDomainRewrite: `${webRtcTarget}`,
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
