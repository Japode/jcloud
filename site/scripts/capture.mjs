// Runs each scenario under site/demo/ with the jcloud named by $JCLOUD and writes what it
// printed to src/transcripts.json. site/scripts/capture.sh is what calls it, with a
// binary built from a release tag; run on its own it captures whatever $JCLOUD is.
//
// stdout and stderr go to one file descriptor, so the lines keep the order a terminal
// shows them in. jcloud's colours are read before they are stripped: green is a pass,
// red a refusal, and an `::error` line is the annotation GitHub Actions turns into a
// mark on the file.
import { spawnSync } from "node:child_process";
import { closeSync, openSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const site = join(dirname(fileURLToPath(import.meta.url)), "..");
const jcloud = process.env.JCLOUD;
if (!jcloud) throw new Error("set JCLOUD to the jcloud binary to capture");

// Each scenario is one verb's story. `cwd` is under demo/, `cmd` is what the page shows
// after the prompt, and `stdin` is fed for a verb that reads one.
const scenarios = [
  { id: "hero", cwd: "hero", cmd: "jcloud check japode.yml" },
  { id: "check", cwd: "check", cmd: "jcloud check japode.yml" },
  { id: "repo", cwd: "repo", cmd: "jcloud check --repo . japode.yml" },
  { id: "agree", cwd: "platform", cmd: "jcloud agree --adapted caddy-adapt.json . services/notes/japode.yml" },
  { id: "compare", cwd: ".", cmd: "jcloud compare platform/services/notes/japode.yml compare/theirs.yml" },
  { id: "up", cwd: "platform", cmd: "jcloud up notes" },
  { id: "survey", cwd: "platform", cmd: "jcloud survey ../survey/observations.txt" },
  { id: "env", cwd: "env", cmd: "jcloud env list" },
];

/** One printed line: its kind from the colour it was printed in, and its text without it. */
function classify(raw) {
  const text = raw.replace(/\x1b\[[0-9;]*m/g, "");
  if (text.startsWith("::error")) return { kind: "annotation", text };
  if (text.trim() === "") return { kind: "blank", text: "" };
  if (raw.includes("\x1b[31m")) return { kind: "err", text };
  if (raw.includes("\x1b[32m")) return { kind: "ok", text };
  return { kind: "out", text };
}

function run(args, cwd) {
  const file = join(tmpdir(), `jcloud-capture-${process.pid}.txt`);
  const fd = openSync(file, "w");
  const r = spawnSync(jcloud, args, { cwd, stdio: ["ignore", fd, fd], env: { PATH: process.env.PATH } });
  closeSync(fd);
  const out = readFileSync(file, "utf8");
  rmSync(file);
  if (r.error) throw r.error;
  return { exit: r.status, out };
}

const version = run(["version"], site).out.trim();
const runs = {};
for (const s of scenarios) {
  const args = s.cmd.split(" ").slice(1);
  const { exit, out } = run(args, join(site, "demo", s.cwd));
  const lines = out.replace(/\n$/, "").split("\n").map(classify);
  runs[s.id] = { cwd: `demo/${s.cwd}`, cmd: s.cmd, exit, lines };
  console.log(`  ${s.id.padEnd(8)} exit ${exit}  ${lines.length} line(s)`);
}

const outFile = join(site, "src", "transcripts.json");
writeFileSync(outFile, `${JSON.stringify({ version, runs }, null, 2)}\n`);
console.log(`transcripts: ${Object.keys(runs).length} run(s) by ${version} -> src/transcripts.json`);
