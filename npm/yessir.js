#!/usr/bin/env node
// Launcher for the npm distribution of yessir. It finds the native binary
// shipped in the platform package npm installed (an optionalDependency of
// this package) and runs it with the same arguments, stdio and exit code.
//
// The Claude Code hook itself never goes through this file: `yessir on`
// registers the native binary's own path, so prompts are answered without
// starting Node.
"use strict";

const { spawnSync } = require("child_process");
const fs = require("fs");
const path = require("path");

const pkg = require("../package.json");
const key = `${process.platform}-${process.arch}`;
const exe = process.platform === "win32" ? "yessir.exe" : "yessir";

// The platform package name is whichever optionalDependency ends in "-<platform>-<arch>".
const platformPkg = Object.keys(pkg.optionalDependencies || {}).find((n) => n.endsWith(`-${key}`));

function fail(msg) {
  process.stderr.write(`yessir: ${msg}\n`);
  process.exit(1);
}

if (!platformPkg) {
  fail(
    `no prebuilt binary for ${key}. Supported: ` +
      Object.keys(pkg.optionalDependencies || {})
        .map((n) => n.replace(/^.*yessir-/, ""))
        .join(", ") +
      `.\nBuild from source instead: go install github.com/rlgdev/yessir/cmd/yessir@latest`
  );
}

let bin;
try {
  bin = require.resolve(`${platformPkg}/bin/${exe}`);
} catch {
  fail(
    `the platform package ${platformPkg} is not installed.\n` +
      `It is an optional dependency; it is skipped when installing with --no-optional / --omit=optional,\n` +
      `or when the lockfile was created on another OS. Reinstall with:\n  npm install -g ${pkg.name}`
  );
}

if (process.platform !== "win32") {
  try {
    fs.accessSync(bin, fs.constants.X_OK);
  } catch {
    try {
      fs.chmodSync(bin, 0o755);
    } catch {
      /* fall through; spawn reports the error */
    }
  }
}

const r = spawnSync(bin, process.argv.slice(2), { stdio: "inherit", windowsHide: true });
if (r.error) fail(`cannot run ${path.normalize(bin)}: ${r.error.message}`);
if (r.signal) process.kill(process.pid, r.signal);
process.exit(r.status === null ? 1 : r.status);
