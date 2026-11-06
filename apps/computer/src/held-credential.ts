import type { IssuedCredential } from './connection.ts'
import type { Paths } from './paths.ts'
import { writeCredential, type Credential } from './store.ts'

// This run's Computer Credential, which every connection presents. A
// rotated credential replaces it only once it is saved in full, so a crash at
// any point leaves the machine with a credential the service accepts: the
// old one until the service confirms the successor, which it does only after
// hearing it was saved, and the successor from then on.
export class HeldCredential {
  private readonly paths: Paths
  private current: Credential
  private saving: Promise<void> = Promise.resolve()

  constructor(paths: Paths, credential: Credential) {
    this.paths = paths
    this.current = credential
  }

  secret(): string {
    return this.current.credential
  }

  // Saves the successor over the credential file, atomically, and presents
  // it from the next connection on. Saves run one at a time.
  replace(issued: IssuedCredential): Promise<void> {
    const next = this.saving.then(async () => {
      const rotated: Credential = { ...this.current, credential: issued.credential, credentialExpiresAt: issued.expiresAt }
      await writeCredential(this.paths, rotated, { force: true })
      this.current = rotated
    })
    this.saving = next.catch(() => {})
    return next
  }
}
