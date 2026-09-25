import { describe, expect, it } from "vitest";

/**
 * Every source file in the project, read as text.
 *
 * Vite's own glob rather than the filesystem, so the test needs no Node types
 * and runs against exactly the files the bundler would see. Keys are project
 * paths, e.g. "/src/app/App.tsx".
 */
const sources: Record<string, string> = import.meta.glob("/src/**/*.{ts,tsx}", {
  query: "?raw",
  import: "default",
  eager: true,
});

const ENTRY = "/src/main.tsx";
const EDITOR = "/src/features/article/editor/";
const EDITOR_BARREL = "/src/features/article/editor/index.ts";

/**
 * `import x from "y"` and `export … from "y"`, and nothing else.
 *
 * `[^;'"]` is what keeps a match inside one statement: a multi-line import
 * list crosses newlines but never a semicolon or a quote, so
 * `export const X = lazy(() => import("./Y"))` cannot be read as a static
 * import of "./Y" -- which is the whole distinction this test turns on.
 */
const STATIC_FROM =
  /^[ \t]*(?:import|export)\s+(?:type\s+)?[^;'"]*?\bfrom\s*["']([^"']+)["']/gm;
const SIDE_EFFECT = /^[ \t]*import\s*["']([^"']+)["']/gm;

function specifiersIn(source: string): string[] {
  const found: string[] = [];
  for (const pattern of [STATIC_FROM, SIDE_EFFECT]) {
    pattern.lastIndex = 0;
    let match = pattern.exec(source);
    while (match !== null) {
      found.push(match[1]);
      match = pattern.exec(source);
    }
  }
  return found;
}

function joinPath(fromFile: string, specifier: string): string {
  const parts = [
    ...fromFile.split("/").slice(0, -1),
    ...specifier.split("?")[0].split("/"),
  ];
  const out: string[] = [];
  for (const part of parts) {
    if (part === "" || part === ".") continue;
    if (part === "..") {
      out.pop();
      continue;
    }
    out.push(part);
  }
  return `/${out.join("/")}`;
}

function resolveModule(importer: string, specifier: string): string | null {
  // Only this project's own files are walked. A package is not what this test
  // is about, and node_modules has no bearing on where our code is split.
  if (!specifier.startsWith(".")) return null;
  const base = joinPath(importer, specifier);
  return (
    [
      base,
      `${base}.ts`,
      `${base}.tsx`,
      `${base}/index.ts`,
      `${base}/index.tsx`,
    ].find((candidate) => candidate in sources) ?? null
  );
}

/** Every file the entry point pulls in without an `import()` in between. */
function staticGraph(entry: string): Set<string> {
  const seen = new Set<string>();
  const queue = [entry];
  while (queue.length > 0) {
    const file = queue.pop();
    if (file === undefined || seen.has(file)) continue;
    seen.add(file);
    for (const specifier of specifiersIn(sources[file])) {
      const next = resolveModule(file, specifier);
      if (next !== null && !seen.has(next)) queue.push(next);
    }
  }
  return seen;
}

/** The writing UI is lazy; the reader shares only content definitions. */
describe("what the first page has to download", () => {
  const graph = staticGraph(ENTRY);

  it("reaches the editor's barrel, so the walk is looking in the right place", () => {
    expect(sources[ENTRY]).toBeTypeOf("string");
    expect(graph.has("/src/app/App.tsx")).toBe(true);
    expect(graph.has(EDITOR_BARREL)).toBe(true);
  });

  it("reaches nothing behind that barrel", () => {
    expect([...graph].filter((file) => file.startsWith(EDITOR))).toEqual([
      EDITOR_BARREL,
    ]);
  });

  it("keeps the barrel itself free of everything it is hiding", () => {
    expect(specifiersIn(sources[EDITOR_BARREL])).toEqual(["react"]);
  });

  it("only reaches Tiptap through the shared content module", () => {
    const offenders = [...graph].filter(
      (file) =>
        !file.startsWith("/src/components/content/") &&
        specifiersIn(sources[file]).some((specifier) =>
          specifier.startsWith("@tiptap/"),
        ),
    );
    expect(offenders).toEqual([]);
  });
});
