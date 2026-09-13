// Fails the build if a production bundle carries dev-only auth code.
//
// Shipped assets (.js/.css/.html) are scanned for runtime evidence of the
// testauth adapter. Sourcemaps are checked differently: they legitimately
// contain select.ts, whose comments and dynamic-import specifier mention
// testauth, so matching text there is a false positive. What matters for a map
// is whether the testauth module's own source was pulled in — which only
// happens if rollup kept the module.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const DIST = fileURLToPath(new URL("../dist/", import.meta.url));

const forbidden = [
  { pattern: /TestAuthProvider/, why: "TestAuthProvider must not be in prod bundle" },
  { pattern: /signInShort/, why: "the e2e short-token hook must not be in prod bundle" },
  { pattern: /["']ta\.token["']/, why: "testauth sessionStorage keys must not be in prod bundle" },
  { pattern: /testauth mint failed/, why: "testauth mint code must not be in prod bundle" },
  { pattern: /\/mint\?sub=/, why: "testauth /mint URL must not be in prod bundle" },
  { pattern: /__ta\b/, why: "the dev auth window handle must not be in prod bundle" },
];

const violations = [];

function checkAsset(path, body) {
  for (const { pattern, why } of forbidden) {
    if (pattern.test(body)) violations.push(`${path}: ${why}`);
  }
}

function checkSourcemap(path, body) {
  let map;
  try {
    map = JSON.parse(body);
  } catch {
    violations.push(`${path}: sourcemap is not valid JSON`);
    return;
  }
  const leaked = (map.sources ?? []).filter((s) => /auth\/testauth\.tsx?$/.test(s));
  if (leaked.length) {
    violations.push(`${path}: testauth module source is in the bundle (${leaked.join(", ")})`);
  }
}

function walk(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) {
      walk(p);
      continue;
    }
    if (!/\.(js|css|html|map)$/.test(name)) continue;
    const rel = relative(DIST, p);
    const body = readFileSync(p, "utf8");
    if (name.endsWith(".map")) checkSourcemap(rel, body);
    else checkAsset(rel, body);
  }
}

walk(DIST);

if (violations.length) {
  console.error("Bundle guard FAILED:");
  for (const v of violations) console.error("  " + v);
  process.exit(1);
}
console.log("Bundle guard OK");
