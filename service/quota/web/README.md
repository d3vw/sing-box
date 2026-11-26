# Quota portal dashboard

The page the `quota-portal` outbound serves (http://6.6.6.6 by default), as a
Svelte 5 app. It builds into one page, `../ui/index.html`, with the script
and styles inlined, beside the files in `public/` it links to (the Archivo
number font, icons, the manifest). `portal_page.go` embeds `../ui` and
serves the page with the page data inlined. The build output is committed, so building sing-box needs no Node.js; rebuild it
after changing anything here:

    make quota_portal        # from the repository root
    # or: npm ci && npm run build

`npm run dev` serves the app with hot reload and proxies the portal's
endpoints to a live portal: http://6.6.6.6, reachable while this machine's
traffic goes through the sing-box server, or `QUOTA_PORTAL=http://host:port`.
Without inlined data the app fetches it from `/quota/api/page`.

- `src/lib/state.svelte.ts`: page data, refresh, theme and language
- `src/lib/i18n.ts`: the Chinese and English text
- `src/lib/nq.svelte.ts`, `conns.svelte.ts`, `outbound.svelte.ts`: the
  network quality test, live connections and the outbound tab's nodes
- `src/app.css`: the styles, global and unscoped
- `src/components/Counter.svelte`: the rolling remaining figure

The look follows the 流量面板 design project (Next.js, mock data): its
color tokens, sizes and motion are carried over as they are.

The `go.mod` here only keeps the go tool out of `node_modules`.
