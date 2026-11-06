// NameTakenError is a Create refused because the name is taken; the person
// may replace that Attachment on purpose instead.
export class NameTakenError extends Error {
  constructor(readonly fileName: string) {
    super(`An attachment named ${fileName} already exists.`);
  }
}
