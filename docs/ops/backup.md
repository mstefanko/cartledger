# Backups

Day-to-day operator guide for creating, storing, and restoring CartLedger
backups. For error-path recovery (failed restores, migration failures, zip-slip
rejections) see [`migration-recovery.md`](./migration-recovery.md).

## Contents

- [What's in a backup](#whats-in-a-backup)
- [What's NOT in a backup](#whats-not-in-a-backup)
- [Encryption — read this before uploading to cloud storage](#encryption--read-this-before-uploading-to-cloud-storage)
- [Retention](#retention)
- [Creating a backup](#creating-a-backup)
- [Downloading, deleting, inspecting](#downloading-deleting-inspecting)
- [Restoring from a backup](#restoring-from-a-backup)
- [Disaster recovery](#disaster-recovery)

---

## What's in a backup

A backup is a single gzipped tar archive containing:

- `cartledger.db` — the full SQLite database, produced by `PRAGMA wal_checkpoint(TRUNCATE)` followed by `VACUUM INTO`. Deterministic and self-consistent at the moment the backup started.
- `receipts/<receipt-id>/*.jpg|*.png` — every stored receipt image and its preprocessed counterpart that was on disk when the backup started.
- `products/**` — any product images the app has saved.
- `manifest.json` — `schema_version`, `app_version`, `created_at`, `missing_images`, and a host identifier. Used by the restore validator.

The archive is plain `tar.gz` — `tar tf backup-*.tar.gz` lists the entries without any special tooling.

## What's NOT in a backup

- **Environment variables.** `ANTHROPIC_API_KEY`, `JWT_SECRET`, `INTEGRATIONS_KEY`, `DATA_DIR`, `PORT`, `ALLOWED_ORIGINS`, etc. are process-level config — they live in your systemd unit / docker-compose file / `.env`, not in `DATA_DIR`. You restore these separately.
- **Integration tokens in plaintext.** Mealie and other integration tokens are stored encrypted-at-rest in the DB with `INTEGRATIONS_KEY`. The archive contains the ciphertext. Without `INTEGRATIONS_KEY` an attacker who steals the backup cannot decrypt those tokens — but this also means you must preserve `INTEGRATIONS_KEY` across a restore or integrations will fail to authenticate.
- **Pruned original images.** The retention janitor (see `IMAGE_RETENTION_DAYS`) may have removed originals older than N days. The DB still references them; the backup records a `missing_images` count in the manifest so you know how many rows lost their image.
- **WAL/SHM sidecar files.** Backup runs a checkpoint first, so the archive's `cartledger.db` is fully self-contained — no `-wal` or `-shm` files needed.
- **The `backups/` subdirectory itself.** Backups do not include prior backups (no recursive archiving).

## Encryption — read this before uploading to cloud storage

**Backups are not encrypted at rest in v1.** Archive contents include:

- Bcrypt password hashes. Slow to crack, but weak passwords are still at risk from an exposed hash.
- Receipt images, which may contain store loyalty numbers, last-4 payment digits, household address (in store banners), etc.
- Encrypted integration tokens (safe without `INTEGRATIONS_KEY`, but a motivated attacker who also controls your host can decrypt them).

**If you back up to trusted local storage** (NAS in the same closet, attached USB, a private LAN server), this is fine as-is.

**If you push backups to untrusted storage** — cloud sync (Dropbox, iCloud, Google Drive, Backblaze B2), a shared drive, anything third-party — **encrypt the archive before uploading**. The two-command workflow:

```sh
# age (recommended — modern, 30-second setup)
age-keygen -o ~/.cartledger-backup.key
age -R ~/.cartledger-backup.key.pub \
    -o backup-20260418T031500Z.tar.gz.age \
    backup-20260418T031500Z.tar.gz

# gpg (if you already have GPG keys set up)
gpg --encrypt --recipient your@email \
    --output backup-20260418T031500Z.tar.gz.gpg \
    backup-20260418T031500Z.tar.gz
```

Restore is the reverse — decrypt to a local path first, then feed that path to `cartledger restore`.

Archive encryption is tracked for v1.1 (see `PLAN-backup-and-export.md`).

## Retention

The app keeps the **5 most recent `complete`** backups on disk and prunes the rest. Configure via env:

```
BACKUP_RETAIN_COUNT=5   # default; integer >= 1
```

Pruning runs at the end of every successful backup. Failed backups are not counted against the retain budget. The prune deletes both the DB row and the on-disk archive.

Off-site / long-term retention is your responsibility — copy archives off the host (see encryption note above) or use a filesystem snapshot tool (ZFS, btrfs, restic).

## Creating a backup

### From the admin UI

Settings → **Data** tab → **Create backup**.

- Button disables and shows a spinner while a run is in progress.
- A row appears in the table at `status=running`; the UI polls every 2s and flips to `complete` (or `failed` with a message) when done.
- Only one backup can run at a time server-wide. A second click while one is running returns HTTP 409.
- Non-admin users get HTTP 403 on every backup endpoint.

### From the CLI

```sh
# Managed mode — writes under $DATA_DIR/backups/, records a DB row,
# participates in retention pruning. Same path as the UI.
cartledger backup

# Legacy / ad-hoc mode — writes to a specific path, bypasses the DB table
# and retention. Useful for cron jobs that pipe the archive over scp/rsync.
cartledger backup --out /mnt/nas/cartledger/backup-$(date -u +%Y%m%dT%H%M%SZ).tar.gz
```

The CLI waits for completion and prints the resulting path + size before exiting 0. On failure it exits non-zero with a stderr message.

## Downloading, deleting, inspecting

- **Download**: Settings → Data → click the download icon on any `complete` row. Uses session cookie auth; the download is a normal `GET` with `Content-Disposition: attachment`.
- **Delete**: trash-can icon on any row. Confirms before removing the row + on-disk file.
- **Inspect locally**: `tar tzf backup-*.tar.gz` lists entries. `tar xOzf backup-*.tar.gz manifest.json | jq .` prints the manifest.
- **Verify DB opens**: `tar xzf backup-*.tar.gz cartledger.db -O | sqlite3 :memory: .schema` — reads without unpacking.

## Restoring from a backup

Restore is the single most destructive operation in the app. It is gated by two protections (UI) or one (CLI):

1. **Admin + password re-auth** (UI only). Cookie auth alone is not enough — the UI prompts for the logged-in admin's password before accepting the upload. A leaked cookie therefore does not wipe your database.
2. **Schema-version guard** (both surfaces). An archive declaring a schema newer than the binary supports is rejected before any files are written.
3. **Archive allowlist** (both surfaces). Zip-slip / symlink / hardlink / absolute-path / `..` entries are rejected. Only `manifest.json`, `cartledger.db`, `receipts/**`, and `products/**` are allowed.

### From the admin UI

Settings → Data → **Restore** → pick archive → enter your password → confirm. On success the UI shows a persistent banner: "Restore staged — restart the server to complete." Apply the restart (systemctl / docker restart / etc.) and the server replays the staged archive on startup.

### From the CLI (disaster recovery)

```sh
cartledger restore /path/to/backup-20260418T031500Z.tar.gz
```

The CLI **refuses** if `$DATA_DIR/cartledger.db` already exists — this is intentional. It is designed for cold-recovery into an empty data directory. Pass `--force` only if you have already moved the existing DB aside (or your cold-recovery plan is to overwrite the broken DB in place).

## Disaster recovery

See [`migration-recovery.md`](./migration-recovery.md) for the full playbook:

- Cold-restore into an empty DATA_DIR.
- Warm-restore when overwriting an existing DATA_DIR.
- Recovery from a staged-restore that failed at server startup.
- Schema-migration failures after a restore.
- Specific archive-rejection errors and how to decode them.
