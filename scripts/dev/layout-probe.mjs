#!/usr/bin/env node
//
// Measure what the browser actually lays out.
//
// The web suite runs on jsdom, which parses and renders nothing: it can assert
// that a declaration exists in a stylesheet and never that two boxes line up,
// that a hover reaches a colour, or that a clip hides anything. Every CSS
// defect this project has shipped was invisible to it and visible to a person,
// so the answer to a geometry question is a browser, not a careful reading.
//
// It drives a headless Chrome over the DevTools protocol against either a
// markup fragment carried by the built stylesheet, or a live page. Hover is
// a real pointer, not a class standing in for one -- a stand-in class is how
// you verify the wrong rule and believe it.

import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { existsSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import process from "node:process";
import { inflateSync } from "node:zlib";

const repository = resolve(import.meta.dirname, "../..");
const assetDirectory = join(repository, "internal/webassets/dist/assets");

const usage = `Measure real layout in a headless browser.

  --fragment <file>   HTML fragment, wrapped in a page carrying the built app CSS
  --url <address>     probe a live page instead of a fragment
  --width <px>        viewport width (default 1280)
  --height <px>       viewport height (default 800)
  --theme <name>      value for data-theme on the document root
  --reduced-motion    emulate prefers-reduced-motion: reduce
  --touch             emulate a touch pointer without hover
  --cookie <n=v>      set this cookie before navigating; repeatable
                      (how a signed-in surface is reached)
  --measure <sel>     report this element's box; repeatable
  --computed <prop>   also report this computed property; repeatable
  --hover <sel>       move a real pointer onto this element first
  --shot <file>       write a screenshot
  --scan <y>          report the first non-background pixel on this row; repeatable
  --eval <js>         evaluate this in the page and report what it returns;
                      repeatable (for asking a page why it is in this state)
  --settle <ms>       keep waiting this long for every selector to appear,
                      and for the pictures on screen to decode before a shot
                      (default 1500 for --url, 0 for --fragment)
  --keep              leave the generated page on disk and print where
  --json              print one JSON document instead of a table

"$name" resolves to the class this build hashed the CSS-module class "name"
into, so a rebuild does not invalidate a probe. It is always the bare class:
write class="$navLink" in markup and ".$navLink" where a selector is wanted.
Run the web build first -- the built stylesheet is what is being measured.

  node scripts/dev/layout-probe.mjs --fragment probe.html --width 1500 \\
    --measure '.$navLink' --measure '.$accountRail' --hover '.$accountRail' \\
    --shot /tmp/rail.png --scan 62 --scan 254
`;

function fail(message) {
  process.stderr.write(`layout-probe: ${message}\n`);
  process.exit(1);
}

function parseArguments(argv) {
  const options = { measure: [], computed: [], scan: [], cookie: [], eval: [], width: 1280, height: 800 };
  for (let index = 0; index < argv.length; index += 1) {
    const flag = argv[index];
    const value = () => {
      const next = argv[index + 1];
      if (next === undefined || next.startsWith("--")) fail(`${flag} needs a value`);
      index += 1;
      return next;
    };
    switch (flag) {
      case "--fragment": options.fragment = value(); break;
      case "--url": options.url = value(); break;
      case "--width": options.width = Number(value()); break;
      case "--height": options.height = Number(value()); break;
      case "--theme": options.theme = value(); break;
      case "--reduced-motion": options.reducedMotion = true; break;
      case "--touch": options.touch = true; break;
      case "--measure": options.measure.push(value()); break;
      case "--computed": options.computed.push(value()); break;
      case "--hover": options.hover = value(); break;
      case "--shot": options.shot = value(); break;
      case "--scan": options.scan.push(Number(value())); break;
      case "--eval": options.eval.push(value()); break;
      case "--settle": options.settle = Number(value()); break;
      case "--cookie": {
        const pair = value();
        const separator = pair.indexOf("=");
        if (separator < 1) fail(`--cookie wants name=value, got ${pair}`);
        options.cookie.push({ name: pair.slice(0, separator), value: pair.slice(separator + 1) });
        break;
      }
      case "--keep": options.keep = true; break;
      case "--json": options.json = true; break;
      case "--help": case "-h": process.stdout.write(usage); process.exit(0); break;
      default: fail(`unknown option ${flag}`);
    }
  }
  if (!options.fragment === !options.url) fail("give exactly one of --fragment or --url");
  if (!Number.isFinite(options.width) || !Number.isFinite(options.height)) fail("width and height must be numbers");
  if (options.scan.some((row) => !Number.isFinite(row))) fail("--scan takes a row number");
  // A fragment is whole the moment it parses. A live page assembles itself,
  // and measuring a selector the app has not rendered yet reports "matched
  // nothing" about a thing that was simply late.
  if (options.settle === undefined) options.settle = options.url ? 1500 : 0;
  if (!Number.isFinite(options.settle)) fail("--settle takes a number of milliseconds");
  if (options.scan.length > 0 && !options.shot) options.shotIsTemporary = true;
  return options;
}

/**
 * The stylesheet the app actually ships.
 *
 * Vite emits one hashed bundle, and reading it rather than the sources is the
 * point: a probe against `.module.css` would measure what was written, and
 * what ships is what the reader sees.
 */
function builtStylesheet() {
  if (!existsSync(assetDirectory)) {
    fail("no built assets. Run `task web:build` (or `pnpm --dir web build`) first.");
  }
  const sheets = readdirSync(assetDirectory).filter((name) => name.endsWith(".css"));
  if (sheets.length === 0) fail("the built assets carry no stylesheet");
  return { path: join(assetDirectory, sheets[0]), css: readFileSync(join(assetDirectory, sheets[0]), "utf8") };
}

/**
 * Turn `$name` into the class this build hashed `name` into.
 *
 * CSS modules rename every class per build, so a probe written against a
 * hashed name is stale the next time anybody runs vite. Naming the source
 * class and resolving it here is what makes a probe worth keeping.
 */
function resolveModuleClasses(text, css) {
  const missing = new Set();
  const resolved = text.replaceAll(/\$([A-Za-z][\w-]*)/g, (whole, name) => {
    const match = css.match(new RegExp(`\\._${name}_[a-z0-9]+_\\d+`));
    if (!match) {
      missing.add(name);
      return whole;
    }
    return match[0].slice(1);
  });
  return { resolved, missing: [...missing] };
}

function chromeBinary() {
  const candidates = [
    process.env.CHROME,
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
  ].filter(Boolean);
  const found = candidates.find((path) => existsSync(path));
  if (!found) fail("no Chrome found. Set CHROME to its path.");
  return found;
}

function freePort() {
  return new Promise((settle, reject) => {
    const server = createServer();
    server.on("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      server.close(() => settle(port));
    });
  });
}

/**
 * The debugging socket of the tab, not of the browser.
 *
 * `/json/version` answers with the browser-level endpoint, which knows about
 * targets and windows and refuses `Page.enable` outright. The domains this
 * probe uses all live on a page.
 */
// A screenshot taken before the pictures decode is not evidence of anything: an
// image still arriving looks exactly like one that is broken, and a reader of
// the shot cannot tell the difference. Wait for the ones the viewport can
// actually see -- a lazy image below the fold never loads, so requiring all of
// them would only ever spend the whole budget and time out.
//
// decode() rather than complete: complete says the bytes arrived, decode says
// the frame is ready to paint, and with decoding="async" those are not the same
// moment. A broken image rejects, which is also an answer.
async function waitForPaintedContent(devtools, budgetMs) {
  const until = Date.now() + budgetMs;
  for (;;) {
    const settled = await devtools.send("Runtime.evaluate", {
      expression: `(async () => {
        try { await document.fonts.ready; } catch {}
        const onscreen = [...document.images].filter((image) => {
          const box = image.getBoundingClientRect();
          return box.width > 0 && box.height > 0 &&
            box.bottom > 0 && box.top < innerHeight && box.right > 0 && box.left < innerWidth;
        });
        await Promise.all(onscreen.map((image) =>
          image.decode ? image.decode().catch(() => {}) : Promise.resolve()));
        await new Promise((paint) => requestAnimationFrame(() => requestAnimationFrame(paint)));
        return onscreen.every((image) => image.complete);
      })()`,
      awaitPromise: true,
      returnByValue: true,
    });
    if (settled.result.value === true) return true;
    if (Date.now() >= until) return false;
    await new Promise((again) => setTimeout(again, 100));
  }
}

/**
 * Count what the page still has in flight.
 *
 * "Loaded" is not "finished": a route that splits its editor into its own
 * chunk fires the load event while that chunk is still arriving, and a shot
 * taken then shows a loading line rather than the thing being measured. Only
 * the page itself knows when it has stopped asking for things.
 */
function trackNetwork(devtools) {
  const inflight = new Set();
  let lastChange = Date.now();
  const opened = (params) => {
    inflight.add(params.requestId);
    lastChange = Date.now();
  };
  const closed = (params) => {
    inflight.delete(params.requestId);
    lastChange = Date.now();
  };
  devtools.on("Network.requestWillBeSent", opened);
  devtools.on("Network.loadingFinished", closed);
  devtools.on("Network.loadingFailed", closed);
  let completed = 0;
  devtools.on("Network.loadingFinished", () => {
    completed += 1;
  });
  return {
    // A page that has not asked for anything yet is not quiet, it is still
    // starting: between the entry script arriving and the chunk it imports
    // being requested there is a gap with nothing in flight, and calling that
    // "finished" photographs a loading spinner.
    quietFor: (ms) => completed > 0 && inflight.size === 0 && Date.now() - lastChange >= ms,
    reset() {
      inflight.clear();
      lastChange = Date.now();
    },
  };
}

async function waitForQuiet(network, budgetMs, quietMs) {
  const until = Date.now() + budgetMs;
  for (;;) {
    if (network.quietFor(quietMs)) return true;
    if (Date.now() >= until) return false;
    await new Promise((again) => setTimeout(again, 50));
  }
}

async function waitForPageEndpoint(port, deadlineMs) {
  const until = Date.now() + deadlineMs;
  for (;;) {
    try {
      const response = await fetch(`http://127.0.0.1:${port}/json/list`);
      if (response.ok) {
        const page = (await response.json()).find(
          (target) => target.type === "page" && target.webSocketDebuggerUrl,
        );
        if (page) return page.webSocketDebuggerUrl;
      }
    } catch {
      // Not listening yet. The deadline below is what gives up.
    }
    if (Date.now() > until) fail("Chrome did not open a debugging port");
    await new Promise((settle) => setTimeout(settle, 50));
  }
}

/** A DevTools connection with just the calls this probe makes. */
function connect(endpoint) {
  const socket = new WebSocket(endpoint);
  const pending = new Map();
  const listeners = new Map();
  let nextId = 0;
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(event.data);
    if (message.id !== undefined) {
      const settle = pending.get(message.id);
      pending.delete(message.id);
      if (!settle) return;
      if (message.error) settle.reject(new Error(message.error.message));
      else settle.resolve(message.result);
      return;
    }
    listeners.get(message.method)?.forEach((listener) => listener(message.params));
  });
  const ready = new Promise((settle, reject) => {
    socket.addEventListener("open", settle, { once: true });
    socket.addEventListener("error", () => reject(new Error("devtools socket failed")), { once: true });
  });
  return {
    ready,
    send(method, params = {}) {
      const id = (nextId += 1);
      return new Promise((resolve_, reject) => {
        pending.set(id, { resolve: resolve_, reject });
        socket.send(JSON.stringify({ id, method, params }));
      });
    },
    once(method) {
      return new Promise((settle) => {
        const listener = (params) => {
          listeners.get(method).delete(listener);
          settle(params);
        };
        if (!listeners.has(method)) listeners.set(method, new Set());
        listeners.get(method).add(listener);
      });
    },
    on(method, listener) {
      if (!listeners.has(method)) listeners.set(method, new Set());
      listeners.get(method).add(listener);
    },
    close() { socket.close(); },
  };
}

/**
 * PNG to pixels, without a dependency.
 *
 * Only what a probe needs: 8-bit RGB or RGBA, no interlacing, no palette.
 * Chrome's screenshots are exactly that, and refusing anything else beats
 * quietly returning the wrong colour.
 */
function decodePng(bytes) {
  let cursor = 8;
  let width = 0;
  let height = 0;
  let colorType = 0;
  const data = [];
  while (cursor < bytes.length) {
    const length = bytes.readUInt32BE(cursor);
    const type = bytes.toString("ascii", cursor + 4, cursor + 8);
    const chunk = bytes.subarray(cursor + 8, cursor + 8 + length);
    if (type === "IHDR") {
      width = chunk.readUInt32BE(0);
      height = chunk.readUInt32BE(4);
      if (chunk[8] !== 8) throw new Error("only 8-bit screenshots are supported");
      colorType = chunk[9];
      if (chunk[12] !== 0) throw new Error("interlaced screenshots are not supported");
    } else if (type === "IDAT") {
      data.push(chunk);
    }
    cursor += 12 + length;
  }
  if (colorType !== 2 && colorType !== 6) throw new Error("only RGB screenshots are supported");
  const channels = colorType === 6 ? 4 : 3;
  const raw = inflateSync(Buffer.concat(data));
  const stride = width * channels;
  const pixels = Buffer.alloc(height * stride);
  let read = 0;
  for (let y = 0; y < height; y += 1) {
    const filter = raw[read];
    read += 1;
    const row = raw.subarray(read, read + stride);
    read += stride;
    const out = pixels.subarray(y * stride, (y + 1) * stride);
    const above = y > 0 ? pixels.subarray((y - 1) * stride, y * stride) : Buffer.alloc(stride);
    for (let x = 0; x < stride; x += 1) {
      const left = x >= channels ? out[x - channels] : 0;
      const up = above[x];
      const upLeft = x >= channels ? above[x - channels] : 0;
      let value = row[x];
      if (filter === 1) value += left;
      else if (filter === 2) value += up;
      else if (filter === 3) value += (left + up) >> 1;
      else if (filter === 4) {
        const estimate = left + up - upLeft;
        const dLeft = Math.abs(estimate - left);
        const dUp = Math.abs(estimate - up);
        const dUpLeft = Math.abs(estimate - upLeft);
        value += dLeft <= dUp && dLeft <= dUpLeft ? left : dUp <= dUpLeft ? up : upLeft;
      }
      out[x] = value & 0xff;
    }
  }
  return { width, height, channels, pixels };
}

/** A selector that matched nothing, with the most common reason named. */
function hoverAdvice(flag, written, resolved) {
  const bare = /^[\w-]+$/.test(resolved);
  return `${flag} matched nothing: ${resolved}`
    + (bare ? `. That is a class name, not a selector -- try ".${written}".` : "");
}

const hex = (r, g, b) => `#${[r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("")}`;

/**
 * The first pixel on a row that is not the page background.
 *
 * This is the question "where does the box actually start" asked of the render
 * rather than of the box model -- a rounded corner, a shadow and a transform
 * all move the edge a reader sees without moving the rect.
 */
function scanRow(image, y) {
  if (y < 0 || y >= image.height) return { row: y, error: "outside the screenshot" };
  const { channels, pixels, width } = image;
  const base = y * width * channels;
  const background = [pixels[base], pixels[base + 1], pixels[base + 2]];
  for (let x = 0; x < width; x += 1) {
    const at = base + x * channels;
    if (pixels[at] !== background[0] || pixels[at + 1] !== background[1] || pixels[at + 2] !== background[2]) {
      return { row: y, x, color: hex(pixels[at], pixels[at + 1], pixels[at + 2]) };
    }
  }
  return { row: y, x: null, color: null };
}

function page({ fragment, theme }) {
  return `<!doctype html>
<html${theme ? ` data-theme="${theme}"` : ""}>
<head><meta charset="utf-8"><link rel="stylesheet" href="app.css"></head>
<body>${fragment}</body></html>`;
}

const measureScript = (selectors, extra) => `(() => {
  const seen = {};
  for (const selector of ${JSON.stringify(selectors)}) {
    const node = document.querySelector(selector);
    if (!node) { seen[selector] = null; continue; }
    const box = node.getBoundingClientRect();
    const style = getComputedStyle(node);
    const entry = {
      left: box.left, top: box.top, right: box.right, bottom: box.bottom,
      width: box.width, height: box.height,
      background: style.backgroundColor, color: style.color,
      display: style.display, visibility: style.visibility,
      // What the clip is actually hiding, which is the question a fold or an
      // ellipsis is really about and one no rect answers.
      overflowing: node.scrollHeight > node.clientHeight || node.scrollWidth > node.clientWidth,
    };
    for (const property of ${JSON.stringify(extra)}) entry[property] = style.getPropertyValue(property);
    seen[selector] = entry;
  }
  return JSON.stringify(seen);
})()`;

async function main() {
  const options = parseArguments(process.argv.slice(2));
  // Read in both modes. A fragment needs the stylesheet injected; a live page
  // already has it, but "$name" has to resolve against something either way,
  // and the built bundle is the same one the server is serving.
  const sheet = builtStylesheet();

  const unresolved = new Set();
  const resolveText = (text) => {
    const { resolved, missing } = resolveModuleClasses(text, sheet.css);
    missing.forEach((name) => unresolved.add(name));
    return resolved;
  };

  const selectors = options.measure.map(resolveText);
  const hoverSelector = options.hover ? resolveText(options.hover) : null;

  let workspace = null;
  let target = options.url;
  if (options.fragment) {
    if (!existsSync(options.fragment)) fail(`no such fragment: ${options.fragment}`);
    workspace = mkdtempSync(join(tmpdir(), "moment-layout-probe-"));
    writeFileSync(join(workspace, "app.css"), sheet.css);
    const markup = resolveText(readFileSync(options.fragment, "utf8"));
    writeFileSync(join(workspace, "page.html"), page({ fragment: markup, theme: options.theme }));
    target = `file://${join(workspace, "page.html")}`;
  }
  if (unresolved.size > 0) {
    fail(`no class in the current build matches: ${[...unresolved].join(", ")}. Rebuild, or check the spelling against the .module.css source.`);
  }

  const profile = mkdtempSync(join(tmpdir(), "moment-layout-chrome-"));
  const port = await freePort();
  const chrome = spawn(chromeBinary(), [
    "--headless=new",
    "--disable-gpu",
    "--hide-scrollbars",
    "--force-device-scale-factor=1",
    `--remote-debugging-port=${port}`,
    `--user-data-dir=${profile}`,
    `--window-size=${options.width},${options.height}`,
    "about:blank",
  ], { stdio: "ignore" });

  let report = null;
  try {
    const endpoint = await waitForPageEndpoint(port, 15_000);
    const devtools = connect(endpoint);
    await devtools.ready;
    await devtools.send("Page.enable");
    await devtools.send("Emulation.setDeviceMetricsOverride", {
      width: options.width, height: options.height, deviceScaleFactor: 1, mobile: false,
    });
    if (options.reducedMotion) {
      await devtools.send("Emulation.setEmulatedMedia", {
        features: [{ name: "prefers-reduced-motion", value: "reduce" }],
      });
    }
    if (options.touch) {
      await devtools.send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 1 });
      await devtools.send("Emulation.setEmitTouchEventsForMouse", { enabled: true, configuration: "mobile" });
    }
    await devtools.send("Network.enable");
    const network = trackNetwork(devtools);

    // A blank page with nothing to say about itself is the worst evidence
    // there is. Whatever the page complained about travels with the report.
    const messages = [];
    await devtools.send("Runtime.enable");
    devtools.on("Runtime.exceptionThrown", (params) => {
      const thrown = params.exceptionDetails;
      messages.push({
        level: "exception",
        text: thrown.exception?.description ?? thrown.text ?? "uncaught exception",
      });
    });
    devtools.on("Runtime.consoleAPICalled", (params) => {
      if (params.type !== "error" && params.type !== "warning") return;
      messages.push({
        level: params.type,
        text: params.args
          .map((argument) => argument.description ?? argument.value ?? argument.type)
          .join(" "),
      });
    });
    if (options.cookie.length > 0) {
      if (!options.url) fail("--cookie needs --url: a fragment is not served from an origin");
      const origin = new URL(options.url);
      for (const cookie of options.cookie) {
        await devtools.send("Network.setCookie", {
          name: cookie.name,
          value: cookie.value,
          domain: origin.hostname,
          path: "/",
        });
      }
    }

    // Set the isolated browser's preference before React reads it. Changing
    // only the DOM attribute would leave theme controls in the opposite mode.
    if (options.theme && options.url) {
      await devtools.send("Page.addScriptToEvaluateOnNewDocument", {
        source: `localStorage.setItem("moment.theme.v1", ${JSON.stringify(options.theme)})`,
      });
    }

    const loaded = devtools.once("Page.loadEventFired");
    await devtools.send("Page.navigate", { url: target });
    await loaded;
    // What the caller named is the thing worth waiting for, so it is waited
    // for first; the network settling afterwards is what makes the shot stable.
    const deadline = Date.now() + options.settle;
    const wanted = [...selectors, ...(hoverSelector ? [hoverSelector] : [])];
    if (wanted.length > 0 && options.settle > 0) {
      for (;;) {
        const present = await devtools.send("Runtime.evaluate", {
          expression: `${JSON.stringify(wanted)}.every((s) => document.querySelector(s) !== null)`,
          returnByValue: true,
        });
        if (present.result.value === true || Date.now() > deadline) break;
        await new Promise((settle) => setTimeout(settle, 100));
      }
    }

    // A page that split a route into its own chunk is still fetching it here.
    let quiet = true;
    if (options.settle > 0) {
      quiet = await waitForQuiet(network, Math.max(deadline - Date.now(), 500), 500);
    }

    // The app sets its saved theme after the site loads. Apply the probe's
    // override once initialization has settled, so it reaches the screenshot.
    if (options.theme && options.url) {
      await devtools.send("Runtime.evaluate", {
        expression: `document.documentElement.dataset.theme = ${JSON.stringify(options.theme)}`,
      });
    }

    if (hoverSelector) {
      const spot = await devtools.send("Runtime.evaluate", {
        expression: `(() => { const n = document.querySelector(${JSON.stringify(hoverSelector)});
          if (!n) return "null";
          const b = n.getBoundingClientRect();
          return JSON.stringify({ x: b.left + b.width / 2, y: b.top + b.height / 2 }); })()`,
        returnByValue: true,
      });
      const point = JSON.parse(spot.result.value);
      if (!point) fail(hoverAdvice("--hover", options.hover, hoverSelector));
      // A real pointer, because a class standing in for :hover is how you
      // verify a rule that is not the one the reader will meet.
      await devtools.send("Input.dispatchMouseEvent", { type: "mouseMoved", x: point.x, y: point.y, buttons: 0 });
      await new Promise((settle) => setTimeout(settle, 250));
    }

    const title = (await devtools.send("Runtime.evaluate", {
      expression: "document.title", returnByValue: true,
    })).result.value;

    const measured = selectors.length
      ? JSON.parse((await devtools.send("Runtime.evaluate", {
          expression: measureScript(selectors, options.computed), returnByValue: true,
        })).result.value)
      : {};

    const evaluated = [];
    for (const expression of options.eval) {
      // Awaited, so an expression can drive the page and then report what it
      // did: typing into a field and waiting for the save that follows is one
      // question, and splitting it across two probes would ask it of two
      // different page loads.
      const answer = await devtools.send("Runtime.evaluate", {
        expression: `(async () => { try { return JSON.stringify(await (${expression})) ?? "undefined"; }
          catch (error) { return "threw: " + error.message; } })()`,
        awaitPromise: true,
        returnByValue: true,
      });
      evaluated.push({ expression, value: answer.result.value });
    }

    let scans = [];
    let painted = null;
    if (options.shot || options.scan.length > 0) {
      painted = await waitForPaintedContent(devtools, Math.max(options.settle, options.url ? 2_000 : 0));
      const shot = await devtools.send("Page.captureScreenshot", { format: "png" });
      const bytes = Buffer.from(shot.data, "base64");
      if (options.shot) writeFileSync(options.shot, bytes);
      if (options.scan.length > 0) {
        const image = decodePng(bytes);
        scans = options.scan.map((row) => scanRow(image, row));
      }
    }

    devtools.close();
    report = {
      viewport: { width: options.width, height: options.height },
      emulation: { reducedMotion: Boolean(options.reducedMotion), touch: Boolean(options.touch) },
      theme: options.theme ?? null,
      target,
      title,
      stylesheet: sheet.path ?? null,
      measured,
      scans,
      screenshot: options.shot ?? null,
      // false means the budget ran out with something on screen still
      // undecoded, so the shot shows less than the page has.
      painted,
      // false means the page was still fetching when the budget ran out.
      quiet,
      // Every error and warning the page reported, in order.
      messages,
      evaluated,
    };
  } finally {
    // Wait for it to go before taking its profile away: Chrome writes on the
    // way out, and removing the directory under it turns a finished probe into
    // an ENOTEMPTY that hides whatever the probe just found.
    const exited = new Promise((settle) => chrome.once("exit", settle));
    chrome.kill();
    await Promise.race([exited, new Promise((settle) => setTimeout(settle, 5_000))]);
    rmSync(profile, { recursive: true, force: true });
    if (workspace && !options.keep) rmSync(workspace, { recursive: true, force: true });
    else if (workspace) process.stderr.write(`layout-probe: page kept at ${workspace}\n`);
  }

  if (options.json) {
    process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
    return;
  }
  process.stdout.write(`viewport ${report.viewport.width}x${report.viewport.height}`
    + `${report.theme ? ` theme=${report.theme}` : ""}\n`);
  for (const [selector, box] of Object.entries(report.measured)) {
    if (!box) {
      process.stdout.write(`  ${selector}\n    ${hoverAdvice("this", options.measure[selectors.indexOf(selector)] ?? selector, selector).replace(/^this matched nothing: \S+/, "matched nothing")}\n`);
      continue;
    }
    process.stdout.write(`  ${selector}\n`
      + `    left ${box.left}  right ${box.right}  width ${box.width}  height ${box.height}\n`
      + `    background ${box.background}${box.overflowing ? "  overflowing" : ""}\n`);
    for (const property of options.computed) {
      process.stdout.write(`    ${property} ${box[property]}\n`);
    }
  }
  for (const scan of report.scans) {
    process.stdout.write(`  row ${scan.row}: `
      + (scan.error ?? (scan.x === null ? "nothing but background" : `first ink at x=${scan.x} ${scan.color}`))
      + "\n");
  }
  if (report.screenshot) process.stdout.write(`  screenshot ${report.screenshot}\n`);
  for (const message of report.messages ?? []) {
    process.stdout.write(`  ${message.level}: ${message.text.split("\n")[0]}\n`);
  }
}

await main();
