# Site

The landing page, with a chat that runs the mock in the browser: `cmd/ollama-mock-wasm` compiled to WebAssembly, wrapped as `fetch` by `public/mock.js`. Served as static assets by a Cloudflare Worker at https://ollama-mock.kinduff.com.

```
npx wrangler dev      # http://localhost:8787, builds the wasm first
npx wrangler deploy
```

`build.sh` (run by wrangler) writes `public/ollama-mock.wasm` and copies `wasm_exec.js` from the same Go, since the two must match.

OG image: edit `og/og.html`, then re-render:

```
chromium --headless --hide-scrollbars --window-size=1200,630 --screenshot=public/og.png "file://$PWD/og/og.html"
```
