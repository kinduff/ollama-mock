// Runs ollama-mock, compiled to WebAssembly, inside the page and answers fetch calls with it.
// Nothing leaves the browser: mockFetch("/api/chat", {...}) never touches the network.
import "./wasm_exec.js";

const go = new Go();
export const ready = WebAssembly.instantiateStreaming(fetch(new URL("ollama-mock.wasm", import.meta.url)), go.importObject)
  .then(({ instance }) => { go.run(instance); });

// configure resets the mock with new options: {latency, models}.
export async function configure(options) {
  await ready;
  globalThis.__ollamaMock.configure(options);
}

// onLog sends each line of the mock's request log to fn.
export async function onLog(fn) {
  await ready;
  globalThis.__ollamaMock.onLog = fn;
}

// mockFetch takes the same arguments as fetch and resolves to a Response whose body streams as
// the mock writes it. Aborting the signal hangs up, as a client would.
export async function mockFetch(input, init) {
  await ready;
  const req = new Request(new URL(input, "http://ollama.mock"), init);
  const url = new URL(req.url);
  const body = req.method === "GET" || req.method === "HEAD" ? null : new Uint8Array(await req.arrayBuffer());
  return new Promise((resolve, reject) => {
    let controller, done = false;
    const stream = new ReadableStream({
      start(c) { controller = c; },
      cancel() { if (!done) { done = true; abort(); } },
    });
    const abort = globalThis.__ollamaMock.serve(
      req.method, url.pathname + url.search, Object.fromEntries(req.headers), body,
      (status, headers) => resolve(new Response([204, 304].includes(status) ? null : stream, { status, headers })),
      (chunk) => { if (!done) controller.enqueue(chunk); },
      (err) => {
        if (done) return;
        done = true;
        if (err) { controller.error(new Error(err)); reject(new Error(err)); } else controller.close();
      },
    );
    req.signal.addEventListener("abort", () => {
      if (done) return;
      done = true;
      abort();
      const err = new DOMException("The operation was aborted.", "AbortError");
      try { controller.error(err); } catch {}
      reject(err);
    });
  });
}
