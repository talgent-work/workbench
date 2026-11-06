#!/usr/bin/env node
// The talgent-computer command. Talgent Computer runs on Node.js 24 or newer;
// this file is written so an older Node still runs it and says so, before
// the program, which needs Node.js 24, is loaded.
const REQUIRED_NODE_MAJOR = 24
const found = process.versions.node
if (Number(found.split('.')[0]) < REQUIRED_NODE_MAJOR) {
  process.stderr.write(
    'Talgent Computer needs Node.js ' + REQUIRED_NODE_MAJOR + ' or newer, but this is Node.js ' + found + '.\n' +
    'Install Node.js ' + REQUIRED_NODE_MAJOR + ' from https://nodejs.org, then run the command again.\n',
  )
  process.exit(1)
}
import('./computer.mjs')
