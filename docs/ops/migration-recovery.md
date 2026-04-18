# Migration & Restore Recovery

This document is the operator's playbook for the rare error paths around
backup restores and post-restore schema migrations. For day-to-day backup and
restore usage see the admin UI Settings → Data tab.

## Contents

- [Disaster recovery — restore from backup](#disaster-recovery--restore-from-backup)
- [Restore failed during startup](#restore-failed-during-startup)
- [Migration fails after restore](#migration-fails-after-restore)
- [Archive rejected — specific errors](#archive-rejected--specific-errors)

---

## Disaster recovery — restore from backup

When the web UI is unreachable (corrupt DB, misconfigured container, etc.)
the CLI is the fallback. It is intentionally strict: it will **refuse** to
run if `$DATA_DIR/cartledger.db` already exists — restoring over a live DB
is destructive and should be an explicit decision.

### Cold restore (empty DATA_DIR)

```sh
# 1. Stop the running server (systemctl stop / docker stop / ...).
# 2. Move or delete the existing DATA_DIR contents.
mv /var/lib/cartledger /var/lib/cartledger.broken

# 3. Point at a fresh DATA_DIR and run the CLI.
export DATA_DIR=/var/lib/cartledger
cartledger restore /path/to/backup-20260418T031500Z.tar.gz

# Expected output:
#   restore complete
#     archive:             /path/to/...tar.gz
#     data_dir:            /var/lib/cartledger
#     files_restored:      N
#     ...

# 4. Start the server; migrations run automatically on first open.
systemctl start cartledger
```

### Warm restore (overwriting an existing DATA_DIR)

Only do this if you have already backed up the existing `cartledger.db`
somewhere else.

```sh
cartledger restore --force /path/to/backup.tar.gz
```

`--force` relaxes both the "DB already exists" refusal and the per-file
`O_EXCL` protection. Every file in the archive will overwrite its
counterpart in DATA_DIR.

---

## Restore failed during startup

The server applies a staged restore (uploaded via the admin UI) on the next
boot, **before** it opens the database. If that step fails you will see an
error log like:

```
level=ERROR msg="restore: staged archive failed re-validation — refusing to boot"
  pending_dir=/var/lib/cartledger/restore-pending
  err="..."
```

The server exits non-zero and does not start. The live DB — if any — is
still exactly as it was before the restore upload. Recovery steps:

1. **Read the error message carefully.** It is one of the specific
   validator messages listed in the [rejection table](#archive-rejected--specific-errors)
   below. If the archive is corrupt or tampered with, don't retry — fix
   the source and re-upload.

2. **Remove the pending dir.** The server will refuse to boot as long as
   `$DATA_DIR/restore-pending/pending.manifest.json` exists.

   ```sh
   rm -rf /var/lib/cartledger/restore-pending
   ```

3. **Restart the server.** Without the pending dir the boot is a normal
   no-op and the live DB is opened as usual.

4. **Rollback with the pre-restore snapshot (only if extraction started).**
   If the error occurred *after* the live DB was moved aside — the log will
   include `pre_restore_path=/var/lib/cartledger/cartledger.db.pre-restore-<timestamp>` —
   put the original DB back:

   ```sh
   cd /var/lib/cartledger
   mv cartledger.db cartledger.db.failed-restore   # if a partial new DB exists
   mv cartledger.db.pre-restore-20260418T031500Z cartledger.db
   # Also move the -wal / -shm companions if they exist.
   rm -rf restore-pending
   systemctl start cartledger
   ```

---

## Migration fails after restore

`cartledger.db` is opened after extraction and migrations run to bring the
schema forward. Two failure modes:

### `schema_version` is newer than the current binary

The validator already rejects this case at stage time with:

```
archive declares schema_version=42 but this binary knows only up to 38;
upgrade cartledger before restoring
```

Fix: upgrade the cartledger binary first, then re-attempt the restore.
Downgrading the DB schema is **not supported** — migrations run forward
only.

### `schema_version` is older than the current binary

This is the expected, supported case. Migrations auto-apply on open. If a
forward migration fails (rare — migrations are tested against representative
datasets) you will see an error like:

```
level=ERROR msg="run migrations" err="..."
```

Recovery:

1. Check the migration file that failed. Logs include the version number.
2. If the migration is safe to skip (e.g. additive index that can be created
   manually later) you can mark it applied in `schema_migrations`:

   ```sh
   sqlite3 /var/lib/cartledger/cartledger.db \
     "UPDATE schema_migrations SET version = <N>, dirty = 0;"
   ```

3. If the migration is not safe to skip, rollback to the pre-restore DB
   (see the rollback steps above) and file an issue with the error output.

---

## Archive rejected — specific errors

The restore validator emits one of the following messages. Each indicates
a specific rejection rule from the restore safety guards; the remediation
column tells you what to do.

| Validator error | Cause | Remediation |
|---|---|---|
| `unsafe tar entry path: ... (absolute or traversal)` | Tar entry has an absolute path (e.g. `/etc/passwd`) or uses `..`. Indicates a tampered or hand-crafted archive. | Do not upload. Re-export from a trusted cartledger installation. |
| `unsafe tar entry path: ... (traversal segment)` | Entry path contains a `..` segment. | Same as above — archive is unsafe. |
| `unsafe tar entry path: ... (escapes data dir)` | Entry path resolves outside DATA_DIR after join. Defense-in-depth against traversal tricks. | Same as above. |
| `unsafe tar entry: ... is a symlink` | Tar header type is symlink. Backup never produces symlinks. | Reject — archive was tampered with. |
| `unsafe tar entry: ... is a hard link` | Tar header type is hardlink. Same reasoning. | Reject — archive was tampered with. |
| `unsafe tar entry: ... is a device/fifo node` | Non-regular file type. | Reject — archive was tampered with. |
| `unsafe tar entry: ... not in allowlist` | Entry name is not `MANIFEST.json`, `cartledger.db`, or under `receipts/` / `products/`. | Re-export from cartledger; do not edit archives by hand. |
| `archive missing MANIFEST.json` | Archive has no manifest entry. | Archive is not a cartledger backup; confirm the file. |
| `archive missing cartledger.db entry` | Archive is missing the DB snapshot. | Same as above. |
| `archive MANIFEST.json unreadable` | JSON in `MANIFEST.json` is malformed. | Archive is corrupted. Use a different backup. |
| `manifest missing schema_version (0)` | Manifest has no `schema_version` field. | Archive predates Phase A or was tampered with. Use a different backup. |
| `manifest missing app_version` | Manifest has no `cartledger_version` field. | Same as above. |
| `archive declares schema_version=N but this binary knows only up to M` | Forward-incompatible archive. | Upgrade cartledger, then retry restore. |
| `cartledger.db does not start with SQLite magic header` | The `cartledger.db` entry isn't an SQLite file. | Archive is corrupted or tampered — do not proceed. |
| HTTP 413 (`archive exceeds the 5GB size limit`) | Upload exceeds the 5GB cap. | Archives that large should be restored via the CLI on the host, not the web UI. |
| HTTP 507 (`insufficient disk space to stage archive`) | Target filesystem ran out of space mid-upload. | Free up space in DATA_DIR's filesystem; retry. |
| HTTP 401 (`password incorrect`) | Re-auth password didn't match the current admin's bcrypt hash. | Re-enter the correct password; if forgotten, use the CLI path (filesystem access = auth). |

---

## Emergency contacts

This is a self-hosted application. If the above steps don't resolve the
failure, file an issue at https://github.com/mstefanko/cartledger/issues
with:

- The full server log from the failed boot (`journalctl -u cartledger`).
- The output of `sqlite3 cartledger.db.pre-restore-<ts> "SELECT version, dirty FROM schema_migrations;"`.
- The `MANIFEST.json` contents from the failing archive (`tar -xzOf backup.tar.gz MANIFEST.json`).

Do **not** attach the archive itself — it contains bcrypt password hashes
and potentially sensitive receipt data.
