import { createHash, randomBytes } from 'node:crypto'
import { readdir, readFile } from 'node:fs/promises'
import { join } from 'node:path'
import { isErrno } from '../store.ts'
import { stateFiles, writeJsonAtomic } from './shim.ts'

// The local proxy token of each process's current run. A run presents it to
// the proxy, which takes it as the run's identity within its Work. Only its
// hash is kept, in the process's state directory, so a run keeps reaching
// the proxy across Talgent Computer restarts. The state directory is the
// record; the memory here only caches it, per Work with live tokens.

export interface TokenRecord {
  run: number
  sha256: string
}

export class RunTokens {
  private readonly dirOf: (workId: string, id: string) => string
  private readonly workDir: (workId: string) => string
  // Per Work, process id to its current run's token record.
  private readonly works = new Map<string, Promise<Map<string, TokenRecord>>>()

  constructor(options: { dirOf: (workId: string, id: string) => string; workDir: (workId: string) => string }) {
    this.dirOf = options.dirOf
    this.workDir = options.workDir
  }

  // A fresh token for a new run, replacing the previous run's. The record is
  // written before the cache learns it, so a scan never misses it.
  async mint(workId: string, id: string, run: number): Promise<string> {
    const token = randomBytes(32).toString('base64url')
    const record: TokenRecord = { run, sha256: sha256(token) }
    writeJsonAtomic(join(this.dirOf(workId, id), stateFiles.proxyToken), record)
    ;(await this.load(workId)).set(id, record)
    return token
  }

  // Forgets a deleted process, and the Work once it holds no tokens.
  async forget(workId: string, id: string): Promise<void> {
    const loading = this.works.get(workId)
    if (loading === undefined) return
    const known = await loading
    known.delete(id)
    if (known.size === 0 && this.works.get(workId) === loading) this.works.delete(workId)
  }

  // The process of workId whose current run token is token, and that run.
  async find(workId: string, token: string): Promise<{ id: string; run: number } | undefined> {
    if (token === '') return undefined
    const hash = sha256(token)
    for (const [id, record] of await this.load(workId)) {
      if (record.sha256 === hash) return { id, run: record.run }
    }
    return undefined
  }

  // The Work's tokens, read from its process state directories once. A Work
  // without processes here is not cached, so probing ids costs no memory.
  private load(workId: string): Promise<Map<string, TokenRecord>> {
    let loading = this.works.get(workId)
    if (loading === undefined) {
      const scanning = this.scan(workId)
      loading = scanning.then((known) => {
        if (known === undefined) {
          if (this.works.get(workId) === loading) this.works.delete(workId)
          return new Map<string, TokenRecord>()
        }
        return known
      })
      this.works.set(workId, loading)
      loading.catch(() => {
        if (this.works.get(workId) === loading) this.works.delete(workId)
      })
    }
    return loading
  }

  private async scan(workId: string): Promise<Map<string, TokenRecord> | undefined> {
    let ids: string[]
    try {
      ids = await readdir(this.workDir(workId))
    } catch (error) {
      if (isErrno(error, 'ENOENT') || isErrno(error, 'ENOTDIR')) return undefined
      throw error
    }
    const known = new Map<string, TokenRecord>()
    for (const id of ids) {
      try {
        const record = JSON.parse(await readFile(join(this.dirOf(workId, id), stateFiles.proxyToken), 'utf8')) as TokenRecord
        if (typeof record.sha256 === 'string' && Number.isSafeInteger(record.run)) known.set(id, record)
      } catch (error) {
        if (!isErrno(error, 'ENOENT') && !isErrno(error, 'ENOTDIR') && !(error instanceof SyntaxError)) throw error
      }
    }
    return known
  }
}

function sha256(value: string): string {
  return createHash('sha256').update(value).digest('hex')
}
