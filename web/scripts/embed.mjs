import { readdir, readFile, writeFile, rm, mkdir } from "node:fs/promises";
import { gzipSync } from "node:zlib";
const source = new URL("../dist/client/", import.meta.url);
const target = new URL(
  "../../internal/platform/webui/static/",
  import.meta.url,
);
await rm(target, { recursive: true, force: true });
await mkdir(target, { recursive: true });
async function copy(dir = "") {
  for (const entry of await readdir(new URL(dir, source), {
    withFileTypes: true,
  })) {
    const path = dir + entry.name;
    if (entry.isDirectory()) {
      await mkdir(new URL(path + "/", target), { recursive: true });
      await copy(path + "/");
    } else if (/\.(html|js|css|svg|ico|woff2)$/.test(path))
      await writeFile(
        new URL(path + ".gz", target),
        gzipSync(await readFile(new URL(path, source)), { level: 9 }),
      );
  }
}
await copy();
console.log("Embedded console assets updated.");
