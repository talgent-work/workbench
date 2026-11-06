// Runs at most max tasks at a time; the rest wait their turn in order.
export class Limit {
  private readonly max: number
  private running = 0
  private readonly waiting: (() => void)[] = []

  constructor(max: number) {
    this.max = max
  }

  async run<T>(task: () => Promise<T>): Promise<T> {
    if (this.running >= this.max) await new Promise<void>((resolve) => this.waiting.push(resolve))
    else this.running += 1
    try {
      return await task()
    } finally {
      // The slot passes straight to the next waiter, or frees.
      const next = this.waiting.shift()
      if (next !== undefined) next()
      else this.running -= 1
    }
  }
}
