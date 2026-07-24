// Tool-level parity: run the npm package's built-in tools on a fixture
// directory and print the results (content + details) for diffing against
// the Go implementation (cmd/dumptools). Temp paths are normalized.
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  createBashTool,
  createEditTool,
  createReadTool,
  createWriteTool,
} from "@earendil-works/pi-agent-core";
import { NodeExecutionEnv } from "@earendil-works/pi-agent-core/node";

const dir = mkdtempSync(join(tmpdir(), "pi-tools-parity-"));
const bigLines = Array.from({ length: 2500 }, (_, i) => `line ${i + 1}`).join("\n");
writeFileSync(join(dir, "big.txt"), bigLines);
writeFileSync(join(dir, "small.txt"), "alpha\nbeta\ngamma");
writeFileSync(
  join(dir, "code.txt"),
  "func main() {\n\tprintln(\"hi\")\n}\n\nfunc other() {\n\tprintln(\"other\")\n}\n",
);
writeFileSync(join(dir, "fuzzy.txt"), "it’s a “smart” file\nplain line\n");

const env = new NodeExecutionEnv({ cwd: dir });
const context = { env };
const tools = {
  bash: createBashTool(),
  read: createReadTool(),
  write: createWriteTool(),
  edit: createEditTool(),
};

function normalize(value) {
  return JSON.parse(
    JSON.stringify(value)
      .replaceAll(dir, "<DIR>")
      .replace(/"fullOutputPath":"[^"]*"/g, '"fullOutputPath":"<TMP>"')
      .replace(/\/tmp\/[A-Za-z0-9._/-]*bash-[A-Za-z0-9._-]*\.log/g, "<TMP>"),
  );
}

async function run(tool, args) {
  const prepared = tools[tool].prepareArguments ? tools[tool].prepareArguments(args) : args;
  try {
    const result = await tools[tool].execute("call1", prepared, undefined, undefined, context);
    return { ok: true, content: normalize(result.content), details: normalize(result.details ?? null) };
  } catch (error) {
    return { ok: false, error: normalize(error instanceof Error ? error.message : String(error)) };
  }
}

const out = {};
out["bash-echo"] = await run("bash", { command: "echo hello; sleep 0.1; echo err >&2" });
out["bash-exit"] = await run("bash", { command: "echo boom; exit 3" });
out["bash-truncate"] = await run("bash", { command: "seq 1 3000" });
out["read-small"] = await run("read", { path: "small.txt" });
out["read-offset-limit"] = await run("read", { path: "small.txt", offset: 2, limit: 1 });
out["read-truncated"] = await run("read", { path: "big.txt" });
out["read-offset-beyond"] = await run("read", { path: "small.txt", offset: 99 });
out["write"] = await run("write", { path: "new/file.txt", content: "written content\n" });
out["edit-basic"] = await run("edit", {
  path: "code.txt",
  edits: [
    { oldText: 'println("hi")', newText: 'println("bye")' },
    { oldText: 'println("other")', newText: 'println("changed")' },
  ],
});
out["edit-fuzzy"] = await run("edit", {
  path: "fuzzy.txt",
  edits: [{ oldText: 'it\'s a "smart" file', newText: "it is a plain file" }],
});
out["edit-notfound"] = await run("edit", { path: "small.txt", edits: [{ oldText: "missing", newText: "x" }] });
out["edit-duplicate"] = await run("edit", { path: "code.txt", edits: [{ oldText: "func", newText: "fn" }] });
out["edit-overlap"] = await run("edit", {
  path: "small.txt",
  edits: [
    { oldText: "alpha\nbeta", newText: "x" },
    { oldText: "beta\ngamma", newText: "y" },
  ],
});
out["edit-legacy"] = await run("edit", { path: "small.txt", oldText: "gamma", newText: "delta", edits: [] });

process.stdout.write(JSON.stringify(out, null, 2));
