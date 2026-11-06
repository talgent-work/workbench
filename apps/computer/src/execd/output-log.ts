import { closeSync, constants, fstatSync, ftruncateSync, openSync, readSync, writeSync } from 'node:fs'
import type { FileHandle } from 'node:fs/promises'

// A process's output log: every byte the process wrote, in order, as records
//   [stream u8][offset u64 BE][length u32 BE][data]
// where offset counts the process's output bytes from its first start. The
// shim is its only writer and appends whole records; a record cut short by a
// crash is the log's unwritten tail.
export const STDOUT = 1
export const STDERR = 2
export const RECORD_HEADER = 13
// Bounds one record so a reader's block always holds a whole record.
export const MAX_RECORD_DATA = 64 * 1024
const READ_BLOCK = RECORD_HEADER + 4 * MAX_RECORD_DATA

export interface LogRecord {
  stream: number
  offset: number
  data: Buffer
}

export function encodeRecord(stream: number, offset: number, data: Buffer): Buffer {
  if (data.length > MAX_RECORD_DATA) throw new Error('output record too large')
  const record = Buffer.allocUnsafe(RECORD_HEADER + data.length)
  record.writeUInt8(stream, 0)
  record.writeBigUInt64BE(BigInt(offset), 1)
  record.writeUInt32BE(data.length, 9)
  data.copy(record, RECORD_HEADER)
  return record
}

// Parses the whole records at the start of block. used is how many bytes
// they take; the rest is an incomplete record.
export function decodeRecords(block: Buffer): { records: LogRecord[]; used: number } {
  const records: LogRecord[] = []
  let used = 0
  while (block.length - used >= RECORD_HEADER) {
    const length = block.readUInt32BE(used + 9)
    if (length > MAX_RECORD_DATA) throw new Error('corrupt output log')
    if (block.length - used < RECORD_HEADER + length) break
    records.push({
      stream: block.readUInt8(used),
      offset: Number(block.readBigUInt64BE(used + 1)),
      data: block.subarray(used + RECORD_HEADER, used + RECORD_HEADER + length),
    })
    used += RECORD_HEADER + length
  }
  return { records, used }
}

// The shim's writer of a process's log. It opens the log read-write and
// writes each record at its position rather than in append mode: Windows
// refuses to truncate a file opened for appending, and a torn tail left by a
// crash is cut before the next record. The shim is the log's only writer.
export class LogAppender {
  // The output offset of the next byte appended.
  offset = 0
  private position = 0
  private readonly fd: number

  constructor(path: string) {
    this.fd = openSync(path, constants.O_RDWR | constants.O_CREAT, 0o600)
    try {
      this.resume()
    } catch (error) {
      closeQuietly(this.fd)
      throw error
    }
  }

  // Appends data as records of at most MAX_RECORD_DATA bytes.
  append(stream: number, data: Buffer): void {
    for (let start = 0; start < data.length; start += MAX_RECORD_DATA) {
      const piece = data.subarray(start, start + MAX_RECORD_DATA)
      const record = encodeRecord(stream, this.offset, piece)
      for (let written = 0; written < record.length;) {
        written += writeSync(this.fd, record, written, record.length - written, this.position + written)
      }
      this.position += record.length
      this.offset += piece.length
    }
  }

  close(): void {
    closeQuietly(this.fd)
  }

  // Resumes after the last whole record, cutting a torn tail.
  private resume(): void {
    const size = fstatSync(this.fd).size
    const header = Buffer.alloc(RECORD_HEADER)
    while (this.position + RECORD_HEADER <= size) {
      if (readSync(this.fd, header, 0, RECORD_HEADER, this.position) !== RECORD_HEADER) break
      const length = header.readUInt32BE(9)
      if (length > MAX_RECORD_DATA || this.position + RECORD_HEADER + length > size) break
      this.offset = Number(header.readBigUInt64BE(1)) + length
      this.position += RECORD_HEADER + length
    }
    if (this.position < size) ftruncateSync(this.fd, this.position)
  }
}

export function closeQuietly(fd: number): void {
  try {
    closeSync(fd)
  } catch {
    // Already closed.
  }
}

// Reads a log forward from a file position. The offset of a position is
// known once the records before it were read; checkpoints remember some of
// them so a later reader can start near any output offset.
export class LogCursor {
  position = 0
  // The output offset at position: every byte before it has been read.
  offset = 0
  private readonly handle: FileHandle
  private readonly checkpoints: { position: number; offset: number }[]

  constructor(handle: FileHandle, checkpoints: { position: number; offset: number }[]) {
    this.handle = handle
    this.checkpoints = checkpoints
  }

  // Moves to the last checkpoint at or before offset.
  seek(offset: number): void {
    let best = { position: 0, offset: 0 }
    for (const checkpoint of this.checkpoints) {
      if (checkpoint.offset <= offset && checkpoint.position >= best.position) best = checkpoint
    }
    this.position = best.position
    this.offset = best.offset
  }

  // The whole records written after position, up to about one block.
  async next(): Promise<LogRecord[]> {
    const block = Buffer.allocUnsafe(READ_BLOCK)
    const { bytesRead } = await this.handle.read(block, 0, READ_BLOCK, this.position)
    const { records, used } = decodeRecords(block.subarray(0, bytesRead))
    if (records.length === 0) return records
    const last = records[records.length - 1]
    this.position += used
    this.offset = last.offset + last.data.length
    this.remember()
    return records
  }

  private remember(): void {
    const last = this.checkpoints[this.checkpoints.length - 1]
    if (last === undefined || this.position - last.position >= 1024 * 1024) {
      this.checkpoints.push({ position: this.position, offset: this.offset })
    }
  }

  close(): Promise<void> {
    return this.handle.close()
  }
}
