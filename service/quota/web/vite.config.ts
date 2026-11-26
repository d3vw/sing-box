import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { viteSingleFile } from "vite-plugin-singlefile";

// The portal is one self-contained page that sing-box embeds (see
// ../portal_modern.go), so the build inlines every script and style into
// ../ui/index.html.
//
// `npm run dev` proxies the portal's endpoints to a live portal, by default
// http://6.6.6.6 (reachable while this machine's traffic goes through the
// sing-box server); set QUOTA_PORTAL to use another.
const portal = process.env.QUOTA_PORTAL || "http://6.6.6.6";

export default defineConfig({
  // The base is absolute: the portal answers on any path, so the fonts and
  // icons in public/ must not resolve against the page's.
  plugins: [svelte(), viteSingleFile({ removeViteModuleLoader: true, overrideConfig: { base: "/" } })],
  build: {
    outDir: "../ui",
    emptyOutDir: true,
    target: "es2020",
    reportCompressedSize: false,
  },
  server: {
    proxy: {
      "/quota/api": portal,
      "/quota/reset": portal,
      "/outbound": portal,
      "/connections": { target: portal, ws: true },
      "/probe": { target: portal, ws: true },
    },
  },
});
