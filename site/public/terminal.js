// A shell with the ollama CLI, answered by the mock running in this tab. Commands go through the
// same HTTP API the real CLI uses, so what they change (pulls, loads, deletes) shows up in every
// tab of the page.
import { mockFetch } from "./mock.js";

export const HOST = "browser:11434";

const USAGE = `Large language model runner

Usage:
  ollama [flags]
  ollama [command]

Available Commands:
  serve       Start ollama
  show        Show information for a model
  run         Run a model
  stop        Stop a running model
  pull        Pull a model from a registry
  list        List models
  ps          List running models
  cp          Copy a model
  rm          Remove a model

Flags:
  -h, --help      help for ollama
  -v, --version   Show version information

Also here: curl localhost:11434/..., clear, history, help.
`;

const REPL_HELP = `Available Commands:
  /clear          Clear session context
  /bye            Exit
  /?, /help       Help for a command
`;

// split reads a command line the way a shell would: spaces separate, quotes group, \ escapes in
// double quotes.
export function split(line) {
  const out = [];
  let cur = "", quote = null, has = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quote) {
      if (c === quote) quote = null;
      else if (c === "\\" && quote === '"' && i + 1 < line.length) cur += line[++i];
      else cur += c;
    } else if (c === '"' || c === "'") {
      quote = c;
      has = true;
    } else if (/\s/.test(c)) {
      if (cur || has) out.push(cur);
      cur = "";
      has = false;
    } else if (c === "\\" && i + 1 < line.length) {
      cur += line[++i];
    } else {
      cur += c;
    }
  }
  if (cur || has) out.push(cur);
  return out;
}

// bytes formats a size like Ollama's format.HumanBytes: 5.2 GB, 274 MB.
export function bytes(n) {
  const units = [["TB", 1e12], ["GB", 1e9], ["MB", 1e6], ["KB", 1e3]];
  for (const [unit, size] of units) {
    if (n >= size) {
      const v = n / size;
      return v >= 10 ? `${Math.round(v)} ${unit}` : `${v.toFixed(1)} ${unit}`;
    }
  }
  return `${n} B`;
}

// human says how far a time is from now: "3 minutes ago", "4 minutes from now".
function human(t) {
  const diff = (new Date(t) - Date.now()) / 1000;
  if (Math.abs(diff) > 50 * 365 * 86400) return "Forever";
  const s = Math.abs(diff), suffix = diff < 0 ? "ago" : "from now";
  const n = (v, unit) => `${v} ${unit}${v === 1 ? "" : "s"} ${suffix}`;
  if (s < 1) return diff < 0 ? "Less than a second ago" : "Less than a second from now";
  if (s < 60) return n(Math.floor(s), "second");
  if (s < 120) return `About a minute ${suffix}`;
  if (s < 3600) return n(Math.floor(s / 60), "minute");
  if (s < 7200) return `About an hour ${suffix}`;
  if (s < 86400) return n(Math.floor(s / 3600), "hour");
  return n(Math.floor(s / 86400), "day");
}

// dur formats nanoseconds like Go's time.Duration.
function dur(ns) {
  const trim = (v) => v.replace(/\.?0+$/, "");
  if (ns >= 1e9) return `${trim((ns / 1e9).toFixed(6))}s`;
  if (ns >= 1e6) return `${trim((ns / 1e6).toFixed(4))}ms`;
  return `${trim((ns / 1e3).toFixed(3))}µs`;
}

function table(head, rows) {
  const widths = head.map((h, i) => Math.max(h.length, ...rows.map((r) => String(r[i]).length)));
  return [head, ...rows].map((r) => r.map((c, i) => String(c).padEnd(widths[i] + 4)).join("").trimEnd()).join("\n") + "\n";
}

async function* ndjson(res) {
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += value;
    let nl;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, nl).trim();
      buf = buf.slice(nl + 1);
      if (line) yield JSON.parse(line);
    }
  }
  if (buf.trim()) yield JSON.parse(buf);
}

class APIError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

// createTerminal wires the shell inside root. onChange runs after every command, since any of
// them may have changed the installed or loaded models.
export function createTerminal(root, { onChange = () => {} } = {}) {
  const $ = (sel) => root.querySelector(sel);
  const shell = $(".shell"), out = $(".shell-out"), input = $(".cmd"), ps1 = $(".ps1"), line = $(".prompt-line");
  const stopButton = $(".ctrl-c"), log = $(".srv-log");
  const history = [];
  let at = 0, running = null, repl = null, queue = Promise.resolve();

  const scroll = () => { shell.scrollTop = shell.scrollHeight; };
  function write(text, cls) {
    const span = document.createElement("span");
    if (cls) span.className = cls;
    span.textContent = text;
    out.appendChild(span);
    scroll();
    return span;
  }
  function link(cmd) {
    const span = write(cmd, "t-link");
    span.onclick = () => run(cmd);
    return span;
  }

  async function request(method, path, body, signal) {
    return mockFetch(path, { method, body: body === undefined ? undefined : JSON.stringify(body), signal });
  }
  // json sends a request and parses the answer, which copy and delete leave empty.
  async function json(method, path, body, signal) {
    const res = await request(method, path, body, signal);
    const text = await res.text();
    const data = text ? JSON.parse(text) : {};
    if (!res.ok) throw new APIError(res.status, data.error);
    return data;
  }

  // MARK: commands

  async function list() {
    const { models } = await json("GET", "/api/tags");
    write(table(["NAME", "ID", "SIZE", "MODIFIED"],
      models.map((m) => [m.name, m.digest.replace(/^sha256:/, "").slice(0, 12), bytes(m.size), human(m.modified_at)])), "t-table");
  }

  async function ps() {
    const { models } = await json("GET", "/api/ps");
    write(table(["NAME", "ID", "SIZE", "PROCESSOR", "CONTEXT", "UNTIL"],
      models.map((m) => [m.name, m.digest.replace(/^sha256:/, "").slice(0, 12), bytes(m.size), "100% GPU", m.context_length, human(m.expires_at)])), "t-table");
  }

  async function pull(name, signal) {
    if (!name) throw new Error("accepts 1 arg(s), received 0");
    const res = await request("POST", "/api/pull", { model: name }, signal);
    if (!res.ok) throw new APIError(res.status, (await res.json()).error);
    const layers = {};
    for await (const d of ndjson(res)) {
      if (d.error) throw new Error(d.error);
      if (!d.digest) {
        write(`${d.status} \n`);
        continue;
      }
      let l = layers[d.digest];
      if (!l) {
        l = layers[d.digest] = { span: write(""), start: performance.now() };
        write("\n");
      }
      const pct = d.total ? Math.floor((d.completed / d.total) * 100) : 100;
      const bar = "█".repeat(Math.round(pct / 100 * 16)).padEnd(16, " ");
      let tail = bytes(d.total);
      if (d.completed < d.total) {
        const rate = d.completed / Math.max((performance.now() - l.start) / 1000, 0.05);
        const eta = rate > 0 ? Math.ceil((d.total - d.completed) / rate) : 0;
        tail = `${bytes(d.completed)}/${bytes(d.total)}  ${bytes(rate)}/s  ${eta}s`;
      }
      l.span.textContent = `${d.status}: ${String(pct).padStart(3)}% ▕${bar}▏ ${tail}`;
      scroll();
    }
  }

  async function show(name, flags) {
    if (!name) throw new Error("accepts 1 arg(s), received 0");
    const d = await json("POST", "/api/show", { model: name });
    if (flags.has("--modelfile")) return write(d.modelfile + "\n");
    if (flags.has("--parameters")) return write((d.parameters || "") + "\n");
    if (flags.has("--template")) return write((d.template || "") + "\n");
    const info = d.model_info || {}, arch = info["general.architecture"];
    const rows = [
      ["architecture", arch],
      ["parameters", d.details?.parameter_size],
      ["context length", info[`${arch}.context_length`]],
      ["embedding length", info[`${arch}.embedding_length`]],
      ["quantization", d.details?.quantization_level],
    ].filter(([, v]) => v !== undefined);
    const section = (title, rows) => `  ${title}\n` + rows.map((r) => "    " + r.map((c, i) => i < r.length - 1 ? String(c).padEnd(20) : c).join("")).join("\n") + "\n\n";
    let text = section("Model", rows) + section("Capabilities", (d.capabilities || []).map((c) => [c]));
    const params = (d.parameters || "").split("\n").filter(Boolean).map((p) => { const i = p.indexOf(" "); return [p.slice(0, i), p.slice(i + 1).trim()]; });
    if (params.length) text += section("Parameters", params);
    write(text);
  }

  // ensure pulls a model the CLI is asked to run but doesn't have, as ollama run does.
  async function ensure(name, signal) {
    try {
      await json("POST", "/api/show", { model: name });
    } catch (e) {
      if (e.status !== 404) throw e;
      await pull(name, signal);
    }
  }

  // chat sends messages to the model and prints the reply as the CLI does: thinking dimmed between
  // "Thinking..." and "...done thinking.", then the answer.
  async function chat(session, signal) {
    const body = { model: session.model, messages: session.messages };
    if (session.think !== undefined) body.think = session.think;
    const res = await request("POST", "/api/chat", body, signal);
    if (!res.ok) throw new APIError(res.status, (await res.json()).error);
    let thinking = false, text = "", dim = null, last = null;
    try {
      for await (const d of ndjson(res)) {
        if (d.error) throw new Error(d.error);
        const m = d.message || {};
        if (m.thinking && !session.hideThinking) {
          if (!thinking) { write("Thinking...\n", "t-dim"); dim = write("", "t-dim"); thinking = true; }
          dim.textContent += m.thinking;
          scroll();
        }
        if (m.content) {
          if (thinking) { write("\n...done thinking.\n\n", "t-dim"); thinking = false; }
          text += m.content;
          write(m.content);
        }
        if (d.done) last = d;
      }
    } finally {
      session.messages.push({ role: "assistant", content: text });
    }
    write(repl ? "\n\n" : "\n");
    if (session.verbose && last) {
      const rate = (n, ns) => `${(n / (ns / 1e9)).toFixed(2)} tokens/s`;
      write([
        `total duration:       ${dur(last.total_duration)}`,
        `load duration:        ${dur(last.load_duration)}`,
        `prompt eval count:    ${last.prompt_eval_count} token(s)`,
        `prompt eval duration: ${dur(last.prompt_eval_duration)}`,
        `prompt eval rate:     ${rate(last.prompt_eval_count, last.prompt_eval_duration)}`,
        `eval count:           ${last.eval_count} token(s)`,
        `eval duration:        ${dur(last.eval_duration)}`,
        `eval rate:            ${rate(last.eval_count, last.eval_duration)}`,
      ].join("\n") + "\n", "t-dim");
    }
  }

  async function runModel(args, signal) {
    const session = { messages: [] }, rest = [];
    for (const a of args) {
      if (a === "--verbose") session.verbose = true;
      else if (a === "--hidethinking") session.hideThinking = true;
      else if (a.startsWith("--think")) {
        const v = a.split("=")[1];
        session.think = v === undefined || v === "true" ? true : v === "false" ? false : v;
      } else rest.push(a);
    }
    const [name, ...prompt] = rest;
    if (!name) throw new Error("requires at least 1 arg(s), only received 0");
    session.model = name;
    await ensure(name, signal);
    if (prompt.length) {
      session.messages.push({ role: "user", content: prompt.join(" ") });
      return chat(session, signal);
    }
    repl = session;
  }

  async function ollama(args, signal) {
    const [cmd, ...rest] = args;
    const flags = new Set(rest.filter((a) => a.startsWith("-")));
    const names = rest.filter((a) => !a.startsWith("-"));
    switch (cmd) {
      case undefined: case "help": case "-h": case "--help":
        return write(USAGE);
      case "-v": case "--version": {
        const { version } = await json("GET", "/api/version");
        return write(`ollama version is ${version}\n`);
      }
      case "serve": case "start":
        return write(`Error: listen tcp ${HOST}: bind: address already in use\n`, "t-err");
      case "list": case "ls":
        return list();
      case "ps":
        return ps();
      case "pull":
        return pull(names[0], signal);
      case "show":
        return show(names[0], flags);
      case "run":
        return runModel(rest, signal);
      case "stop":
        if (!names[0]) throw new Error("accepts 1 arg(s), received 0");
        await json("POST", "/api/generate", { model: names[0], keep_alive: 0 });
        return;
      case "rm":
        if (!names.length) throw new Error("requires at least 1 arg(s), only received 0");
        for (const n of names) {
          await json("DELETE", "/api/delete", { model: n });
          write(`deleted '${n}'\n`);
        }
        return;
      case "cp":
        if (names.length !== 2) throw new Error(`accepts 2 arg(s), received ${names.length}`);
        await json("POST", "/api/copy", { source: names[0], destination: names[1] });
        return write(`copied '${names[0]}' to '${names[1]}'\n`);
      case "create": case "push": case "signin": case "signout":
        return write(`ollama ${cmd} isn't in this demo terminal. The mock serves its API; see the README.\n`, "t-dim");
      default:
        throw new Error(`unknown command "${cmd}" for "ollama"`);
    }
  }

  async function curl(args, signal) {
    let method, data, url;
    for (let i = 0; i < args.length; i++) {
      const a = args[i];
      if (a === "-X" || a === "--request") method = args[++i];
      else if (["-d", "--data", "--data-raw", "--json"].includes(a)) data = args[++i];
      else if (a === "-H" || a === "--header") i++;
      else if (!a.startsWith("-")) url = a;
    }
    if (!url) return write("curl: try 'curl --help' for more information\n", "t-err");
    const u = new URL(/^https?:\/\//.test(url) ? url : `http://${url}`);
    if (!["localhost:11434", "127.0.0.1:11434", HOST].includes(u.host)) {
      return write(`curl: (7) Failed to connect to ${u.host}: this terminal only reaches the mock on ${HOST}\n`, "t-err");
    }
    const res = await mockFetch(u.pathname + u.search, { method: method || (data !== undefined ? "POST" : "GET"), body: data, signal });
    const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
    let last = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      write(value);
      last = value;
    }
    if (!last.endsWith("\n")) write("%\n", "t-inv");
  }

  async function dispatch(args, signal) {
    const [cmd, ...rest] = args;
    switch (cmd) {
      case "ollama": return ollama(rest, signal);
      case "curl": return curl(rest, signal);
      case "clear": out.replaceChildren(); return;
      case "history": return write(history.map((h, i) => `${String(i + 1).padStart(5)}  ${h}`).join("\n") + "\n");
      case "help": return write(USAGE);
      case "ollama-mock": return write(`ollama-mock is already running on ${HOST}. Its log is above.\n`, "t-dim");
      case "exit": return write("This tab is the shell; it stays open.\n", "t-dim");
      default: return write(`zsh: command not found: ${cmd}\n`, "t-err");
    }
  }

  async function replLine(text, signal) {
    const t = text.trim();
    if (!t) return;
    if (t === "/bye") { repl = null; return; }
    if (t === "/clear") { repl.messages = []; return write("Cleared session context\n"); }
    if (t === "/?" || t === "/help") return write(REPL_HELP);
    if (t.startsWith("/")) return write(`Unknown command '${t.split(" ")[0]}'. Type /? for help\n`);
    repl.messages.push({ role: "user", content: text });
    return chat(repl, signal);
  }

  // MARK: prompt

  function prompt() {
    ps1.textContent = repl ? ">>> " : "$ ";
    input.placeholder = repl ? "Send a message (/? for help)" : "";
  }

  // idle shows the prompt, or hides it while a command runs and keeps focus on the shell so
  // Ctrl+C still lands.
  function idle(on) {
    const focused = root.contains(document.activeElement);
    line.hidden = !on;
    stopButton.hidden = on;
    if (on) prompt();
    if (focused || on && root.offsetParent) (on ? input : shell).focus({ preventScroll: true });
    scroll();
  }

  // exec runs one line as typed at the prompt.
  async function exec(text) {
    const inRepl = !!repl;
    write(inRepl ? ">>> " : "$ ", inRepl ? "t-dim" : "t-ps");
    write(text + "\n");
    if (!inRepl && !text.trim()) return;
    if (!inRepl) history.push(text);
    at = history.length;
    running = new AbortController();
    idle(false);
    try {
      if (inRepl) await replLine(text, running.signal);
      else await dispatch(split(text), running.signal);
    } catch (e) {
      if (e.name === "AbortError") write("^C\n");
      else write(`Error: ${e.message}\n`, "t-err");
    } finally {
      running = null;
      idle(true);
      onChange();
    }
  }

  // run types a command at the prompt and runs it, after whatever is running now.
  function run(cmd) {
    queue = queue.then(async () => {
      if (repl) { repl = null; write(">>> /bye\n", "t-dim"); }
      await exec(cmd);
    });
    return queue;
  }

  input.onkeydown = (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      const text = input.value;
      input.value = "";
      exec(text);
    } else if (e.key === "ArrowUp" && !repl && at > 0) {
      e.preventDefault();
      input.value = history[--at];
    } else if (e.key === "ArrowDown" && !repl) {
      e.preventDefault();
      at = Math.min(at + 1, history.length);
      input.value = history[at] ?? "";
    } else if (e.key === "l" && e.ctrlKey) {
      e.preventDefault();
      out.replaceChildren();
    } else if (e.key === "d" && e.ctrlKey && repl && !input.value) {
      e.preventDefault();
      repl = null;
      write(">>> \n", "t-dim");
      prompt();
    } else if (e.key === "c" && e.ctrlKey && !window.getSelection().toString()) {
      e.preventDefault();
      write((repl ? ">>> " : "$ ") + input.value + "^C\n", "t-dim");
      input.value = "";
      if (repl) write("Use Ctrl + d or /bye to exit.\n");
    }
  };
  shell.tabIndex = -1;
  document.addEventListener("keydown", (e) => {
    if (running && root.offsetParent && e.key === "c" && e.ctrlKey && !window.getSelection().toString()) {
      e.preventDefault();
      running.abort();
    }
  });
  stopButton.onclick = () => running?.abort();
  shell.onclick = () => { if (!running && !window.getSelection().toString()) input.focus({ preventScroll: true }); };

  return {
    run,
    write,
    link,
    focus: () => { if (!running) input.focus({ preventScroll: true }); },
    // log appends one line of the mock's request log, stamped with local time.
    log(text, cls) {
      const stick = log.scrollHeight - log.scrollTop - log.clientHeight < 20;
      const span = document.createElement("span");
      if (cls) span.className = cls;
      span.textContent = text.replace(/^\d\d:\d\d:\d\d/, new Date().toTimeString().slice(0, 8));
      log.appendChild(span);
      while (log.childNodes.length > 300) log.firstChild.remove();
      if (stick) log.scrollTop = log.scrollHeight;
    },
    ready() { idle(true); },
  };
}
