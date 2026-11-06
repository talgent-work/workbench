---
name: using-platform-api
description: Publish, replace or delete this Work's Artifacts and search or download the owner's Personal Library over Talgent's HTTP API. Use when the Work has no talgent MCP tools for these.
---

# Using the platform API

Talgent's API for this Work answers at `$TALGENT_API_ADDRESS`. When the Work has the `talgent` MCP server's Artifact and Library tools, use those; this skill is for a Work without them. When `$TALGENT_API_ADDRESS` is unset or empty, as on a Managed Computer, the Work cannot reach the platform this way: tell your owner that publishing and the Library are unavailable here.

## Calling it

Every call is a JSON `POST` to `$TALGENT_API_ADDRESS/<procedure>`:

```sh
curl -sS -X POST "$TALGENT_API_ADDRESS/talgent.storage.private.v1.ArtifactService/CreateArtifact" \
  -H 'Content-Type: application/json' \
  -d '{"name": "report.pdf", "path": "dist/report.pdf"}'
```

The sandbox adds this Work's identity and credentials to every request it sends there. Send only `Content-Type` and the body. A success answers HTTP 200 with a JSON body; a refusal answers another status with `{"code": "...", "message": "..."}`.

## Artifacts of this Work

An Artifact is a file of this Work published for its owner; it reaches nobody until published. `path` is one file relative to `$TALGENT_WORK_ROOT`.

| Procedure | Body | Does |
|---|---|---|
| `talgent.storage.private.v1.ArtifactService/CreateArtifact` | `{"name": "<file name>", "path": "<path>"}` | Publishes the file as a new Artifact named `name` |
| `talgent.storage.private.v1.ArtifactService/ReplaceArtifact` | `{"name": "<file name>", "path": "<path>"}` | Replaces an existing Artifact's content; its name and share links stay |
| `talgent.storage.private.v1.ArtifactService/DeleteArtifact` | `{"name": "<file name>"}` | Deletes the Artifact; its share links end and the source stays in the workspace |

Each answers `{"outcome": {"artifact": {"name": ..., "sizeBytes": ...}}}`, and DeleteArtifact `{}`.

## The Personal Library

The Personal Library is the Artifacts of your owner's Works and of the Works shared with them. Only the Agent of a Work done for a person, without a Project, reads it.

1. **Search**: `talgent.agent.public.v1.PersonalWorkService/ListLibraryFiles` with `{"nameQuery": "<part of a name>"}`, and `"workId"` to narrow to one Work; both are optional. It answers `files`, newest first, each with `workId`, `name`, `sizeBytes`, `contentType` and `addedAt`, and `works` with each Work's `title`.
2. **Get a link**: `talgent.agent.public.v1.WorkFileService/CreateWorkFileAccessLink` with `{"workId": "<workId>", "kind": "WORK_FILE_KIND_ARTIFACT", "name": "<name>", "disposition": "WORK_FILE_ACCESS_DISPOSITION_DOWNLOAD"}`. It answers `accessPath` and `expiresAt`.
3. **Download** within the link's lifetime, outside `inputs/`:

   ```sh
   curl -fsS -o "$TALGENT_WORK_ROOT/<path>" "$TALGENT_API_ADDRESS<accessPath>"
   ```

The copy is a snapshot for reference: it is not an Artifact of this Work and does not follow later changes.

## Refusals

| `code` | Meaning | Next |
|---|---|---|
| `already_exists` | An Artifact already has this name | ReplaceArtifact to update it on purpose, or another name |
| `not_found` | No Artifact or Library file by this name, or the source file is missing | Check the name, or the path under `$TALGENT_WORK_ROOT` |
| `invalid_argument` | The source is not one regular file under `$TALGENT_WORK_ROOT` outside `inputs/` and `knowledge/`, reached without symbolic links; or the name is not one file name | Pack several files into one archive; name it without slashes |
| `permission_denied` | This Work may not make the call, such as a Library read from a Work of a Project | Tell your owner what you could not do |
| `unauthenticated` | The Work's credential is not current, as while it stops | Stop calling; the call will not succeed in this session |
