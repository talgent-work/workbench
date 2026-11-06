// Bundles Talgent Computer, ws included, into one self-contained file beside
// the talgent-computer command, which checks the Node.js version first. The
// npm package ships dist/ and installs Claude Code as its dependency.
import { chmod, copyFile } from 'node:fs/promises'
import { build } from 'esbuild'

await build({
  entryPoints: ['src/main.ts'],
  outfile: 'dist/computer.mjs',
  bundle: true,
  platform: 'node',
  target: 'node24',
  format: 'esm',
  // ws is CommonJS and requires node built-ins; esbuild's ESM output needs a
  // real require for them. bufferutil and utf-8-validate are optional native
  // speedups ws loads inside try/catch, so they stay out of the bundle.
  banner: {
    js: [
      "import { createRequire as __talgentCreateRequire } from 'node:module';",
      'const require = __talgentCreateRequire(import.meta.url);',
    ].join('\n'),
  },
  external: ['bufferutil', 'utf-8-validate'],
  legalComments: 'none',
  logLevel: 'info',
})

await copyFile('src/launcher.mjs', 'dist/talgent-computer.mjs')
await chmod('dist/talgent-computer.mjs', 0o755)
