---
name: using-workspace
description: Find and place the Work's files - inputs, repositories, drafts, deliverables, personal memory - in the directory each belongs to. Use before reading or writing a file, and before keeping anything past this Work.
---

# Using the workspace

Each directory of a Work holds one kind of file and lasts as long as its owner keeps it. Put every file where its kind belongs. The Work's root is `$TALGENT_WORK_ROOT`: `/workspace` on a Cloud, a directory of its own on a Managed Computer. Reach every Work directory through that variable, never a fixed path.

## The directories

| Directory | Holds | You | Lasts |
|---|---|---|---|
| `$TALGENT_WORK_ROOT` | this Work's own files: drafts, builds, notes | write | a Work with a Session Workspace: while the Work exists, browsable by people, reclaimed after it closes. Any other Work: until the sandbox ends |
| `$TALGENT_WORK_ROOT/inputs` | the Work's Attachments, kept current | read | a Work with a Session Workspace |
| `$TALGENT_WORK_ROOT/repos` | the checked-out repositories, each on its checked-out branch | write | a Work with a Session Workspace, with its workspace |
| `$HOME` | on a Cloud, your HOME: personal memory and configuration, shared by every Work of your Agent, including those running now. On a Managed Computer, the machine owner's own home | write on a Cloud; leave it as it is on a Managed Computer | across Works |

## What goes where

- **A deliverable**: drafted under `$TALGENT_WORK_ROOT`, outside `inputs/`, then published as an Artifact. A workspace file reaches nobody until it is published.
- **A change to a repository**: committed on its branch under `$TALGENT_WORK_ROOT/repos`, following the repository's own conventions.
- **An Attachment you would change**: copied into `$TALGENT_WORK_ROOT`; the change is published as a new Artifact or agreed with whoever owns the Attachment.
- **A Project's facts**: where a Work of a Project's product context says they are.
- **How you work, for later Works**: your memory in HOME on a Cloud. Project facts stay with the Project, so HOME keeps only what holds for you across Projects and Works. On a Managed Computer nothing outside the Work's directories is yours to change.

Done when every file you wrote sits in the directory its kind belongs to, and whatever must outlast this Work is published, committed or, on a Cloud, in HOME.
