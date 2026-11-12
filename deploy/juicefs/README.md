# JuiceFS metadata: backup and restore

The JuiceFS file system holding Attachments, Artifacts, previews and Marketplace archives keeps
its metadata in a dedicated Redis database (`metaurl` in Secret `talgent-juicefs-secret`) and its
data in an S3-compatible bucket. That Redis needs `appendonly yes` and
`maxmemory-policy noeviction`: an evicted key or a lost write corrupts the file system. No
business key may use that database. Persistence is not a backup; keep backups on other storage.

On first mount the CSI driver formats an empty volume from the Secret. A restored volume is
loaded, never formatted.

## Back up

A recovery point needs every writer stopped, so its parts agree:

1. Stop all Works through the product, wait until their sandboxes are gone, and stop controlplane
   and every other writer of the databases or files.
2. Back up the MySQL databases. Back up the NFS export too if Agent sessions must be resumable:
   HOMEs and workspaces live there, not in JuiceFS.
3. With the JuiceFS 1.3.1 client, from a machine that reaches the metadata Redis and the bucket:

   ```bash
   export JUICEFS_META_URL=<metaurl from the Secret, without credentials>
   read -rs REDIS_PASSWORD && export REDIS_PASSWORD   # if that Redis has one
   scripts/juicefs/metadata.sh backup "$backup_dir/juicefs.bin"
   ```

4. Copy the **whole bucket prefix** of the volume to `$backup_dir/objects` with the object
   store's own tool. A copy of the mounted files does not replace metadata plus objects.
5. Store metadata, objects and database backups together as one recovery point, on other
   storage; keep the bucket credentials separately (metadata excludes them).

Done when the recovery point holds all three parts taken in the same stopped window.

## Restore

1. Prepare an **empty** Redis database with the same persistence settings. `load` stops on a
   non-empty database; leave existing data in place and pick another database.
2. Restore the objects into the target bucket and the MySQL databases; keep the source volume
   stopped so two metadata copies never write one prefix.
3. Load and point the volume at the bucket:

   ```bash
   export JUICEFS_META_URL=redis://<restored-metadata>:6379/<db>
   scripts/juicefs/metadata.sh restore "$backup_dir/juicefs.bin"
   juicefs config "$JUICEFS_META_URL" --storage s3 --bucket "$JUICEFS_BUCKET" \
     --access-key "$ACCESS_KEY" --secret-key "$SECRET_KEY"
   juicefs fsck "$JUICEFS_META_URL"
   ```

4. Point `talgent-juicefs-secret` at the restored Redis and bucket, unmount every old client, and
   mount through new PersistentVolumes (new `volumeHandle`) and claims; a running mount keeps the
   old Secret.

Done when downloads of Attachments and Artifacts match their backed-up bytes, and, with the NFS
export restored, a resumed Work continues its earlier conversation.

Sources: [Redis best practices](https://juicefs.com/docs/community/redis_best_practices/),
[metadata backup and recovery](https://juicefs.com/docs/community/metadata_dump_load/).
