import pkg from '../package.json' with { type: 'json' }

export const COMPUTER_VERSION: string = pkg.version
