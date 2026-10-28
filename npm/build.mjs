#!/usr/bin/env node
// Assembles the npm packages for one release.
//
//   node npm/build.mjs --version 0.1.0 --bin-dir dist-bin --out npm/dist [--scope @rlgdev]
//
// --bin-dir must hold one native binary per target, named
//   yessir-<goos>-<goarch>[.exe]   e.g. yessir-windows-amd64.exe, yessir-darwin-arm64
// (missing targets are skipped with a warning, so a local build of a single
// platform works for testing).
//
// Output: <out>/yessir/ (the package users install) and <out>/yessir-<os>-<cpu>/
// (one per platform, each with a single binary). Publish the platform
// packages first, then the main one.
import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");

const args = Object.fromEntries(
  process.argv
    .slice(2)
    .reduce((acc, a, i, all) => (a.startsWith("--") ? [...acc, [a.slice(2), all[i + 1]]] : acc), [])
);
const version = (args.version || readFileSync(join(root, "VERSION"), "utf8")).trim().replace(/^v/, "");
const binDir = resolve(args["bin-dir"] || "dist-bin");
const out = resolve(args.out || join(here, "dist"));
const scope = (args.scope || process.env.NPM_SCOPE || "@rlgdev").replace(/\/$/, "");

if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(version)) {
  console.error(`bad version: ${version}`);
  process.exit(2);
}

// npm (process.platform/arch) <- Go (GOOS/GOARCH)
const targets = [
  { os: "win32", cpu: "x64", goos: "windows", goarch: "amd64" },
  { os: "win32", cpu: "arm64", goos: "windows", goarch: "arm64" },
  { os: "darwin", cpu: "x64", goos: "darwin", goarch: "amd64" },
  { os: "darwin", cpu: "arm64", goos: "darwin", goarch: "arm64" },
  { os: "linux", cpu: "x64", goos: "linux", goarch: "amd64" },
  { os: "linux", cpu: "arm64", goos: "linux", goarch: "arm64" },
];

const common = {
  version,
  license: "MIT",
  author: "rlgdev",
  homepage: "https://github.com/rlgdev/yessir#readme",
  repository: { type: "git", url: "git+https://github.com/rlgdev/yessir.git" },
  bugs: { url: "https://github.com/rlgdev/yessir/issues" },
};

const write = (p, obj) => writeFileSync(p, JSON.stringify(obj, null, 2) + "\n");

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });

const optionalDependencies = {};
const built = [];
for (const t of targets) {
  const exe = t.goos === "windows" ? "yessir.exe" : "yessir";
  const src = join(binDir, `yessir-${t.goos}-${t.goarch}${t.goos === "windows" ? ".exe" : ""}`);
  const name = `${scope}/yessir-${t.os}-${t.cpu}`;
  optionalDependencies[name] = version;
  if (!existsSync(src)) {
    console.warn(`skip ${name}: ${src} not found`);
    continue;
  }
  const dir = join(out, `yessir-${t.os}-${t.cpu}`);
  mkdirSync(join(dir, "bin"), { recursive: true });
  copyFileSync(src, join(dir, "bin", exe));
  chmodSync(join(dir, "bin", exe), 0o755);
  write(join(dir, "package.json"), {
    name,
    description: `The ${t.os}-${t.cpu} binary for ${scope}/yessir.`,
    ...common,
    os: [t.os],
    cpu: [t.cpu],
    files: ["bin"],
    preferUnplugged: true,
  });
  writeFileSync(
    join(dir, "README.md"),
    `# ${name}\n\nThe ${t.os}-${t.cpu} binary of [yessir](https://github.com/rlgdev/yessir). Install \`${scope}/yessir\` instead; npm picks this package automatically.\n`
  );
  built.push(dir);
}

const main = join(out, "yessir");
mkdirSync(join(main, "bin"), { recursive: true });
copyFileSync(join(here, "yessir.js"), join(main, "bin", "yessir.js"));
chmodSync(join(main, "bin", "yessir.js"), 0o755);
copyFileSync(join(root, "README.md"), join(main, "README.md"));
copyFileSync(join(root, "LICENSE"), join(main, "LICENSE"));
write(join(main, "package.json"), {
  name: `${scope}/yessir`,
  description: "Answer Claude Code permission prompts with yes, globally or per folder, without bypass or auto mode.",
  ...common,
  keywords: ["claude", "claude-code", "hooks", "permissions", "auto-approve", "cli"],
  bin: { yessir: "bin/yessir.js" },
  files: ["bin", "README.md", "LICENSE"],
  engines: { node: ">=16" },
  optionalDependencies,
});

console.log(`built ${scope}/yessir@${version} + ${built.length} platform package(s) in ${out}`);
for (const d of [...built, main]) console.log("  " + d);
