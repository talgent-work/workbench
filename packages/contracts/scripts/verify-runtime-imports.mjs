import { readdir } from "node:fs/promises";
import { join, relative, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const packageRoot = new URL("../", import.meta.url);
const distDir = new URL("../dist/", import.meta.url);

async function collectJsFiles(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = [];

  for (const entry of entries) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...await collectJsFiles(path));
    } else if (entry.isFile() && entry.name.endsWith(".js")) {
      files.push(path);
    } else if (entry.isFile() && !entry.name.endsWith(".d.ts") && !entry.name.endsWith(".js.map")) {
      throw new Error(`Unexpected file in generated runtime output: ${path}`);
    }
  }

  return files;
}

const packageRootPath = fileURLToPath(packageRoot);
const distPath = fileURLToPath(distDir);
const jsFiles = await collectJsFiles(distPath);

for (const file of jsFiles) {
  await import(pathToFileURL(file).href);

  const subpath = relative(join(packageRootPath, "dist"), file)
    .slice(0, -3)
    .split(sep)
    .join("/");
  await import(`@talgent/contracts/${subpath}`);
}
