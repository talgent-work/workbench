import { readFile, rm, writeFile } from 'node:fs/promises'

// The running "talgent-computer run" and the process that started it, so a
// platform service manager that ends only its own process (Windows' Task
// Scheduler ends the console host, not the run its batch file started) can
// end the run without ending its Work processes, which descend from it.
// Where the record lives, under Talgent Computer's data directory.
export const RUN_RECORD_PATH = ['state', 'run.json'] as const

export interface RunRecord {
  pid: number
  ppid: number
}

export async function recordRun(file: string): Promise<void> {
  await writeFile(file, JSON.stringify({ pid: process.pid, ppid: process.ppid } satisfies RunRecord))
}

// Forgets the record if it is still this run's.
export async function forgetRun(file: string): Promise<void> {
  const record = await readRun(file)
  if (record?.pid === process.pid) await rm(file, { force: true })
}

export async function readRun(file: string): Promise<RunRecord | undefined> {
  try {
    const value = JSON.parse(await readFile(file, 'utf8')) as Partial<RunRecord>
    return Number.isInteger(value.pid) && Number.isInteger(value.ppid) ? { pid: value.pid as number, ppid: value.ppid as number } : undefined
  } catch {
    return undefined
  }
}
