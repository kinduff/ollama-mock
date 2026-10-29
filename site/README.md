# Site

The landing page, with a chat that runs the mock in the browser: `cmd/ollama-mock-wasm` compiled to WebAssembly, wrapped as `fetch` by `public/mock.js`. Served as static assets by a Cloudflare Worker.

```
npx wrangler dev      # http://localhost:8787, builds the wasm first
npx wrangler deploy
```

`build.sh` (run by wrangler) writes `public/ollama-mock.wasm` and copies `wasm_exec.js` from the same Go, since the two must match.
