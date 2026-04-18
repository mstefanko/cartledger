import { get, post, del, postMultipart } from './client'

// Mirrors internal/models/backup.go Backup row as serialized by
// GET /api/v1/backups. All time fields are ISO 8601 strings from Go's
// time.Time JSON encoding.
export interface Backup {
  id: string
  status: 'running' | 'complete' | 'failed'
  filename: string
  size_bytes: number | null
  schema_version: number
  missing_images: number
  error: string | null
  created_at: string
  completed_at: string | null
}

export async function listBackups(): Promise<Backup[]> {
  return get<Backup[]>('/backups')
}

export async function createBackup(): Promise<{ id: string }> {
  return post<{ id: string }>('/backups')
}

export async function deleteBackup(id: string): Promise<void> {
  return del<void>(`/backups/${encodeURIComponent(id)}`)
}

// Returns the absolute path for a <a href download> anchor. Cookie auth
// covers the GET; we deliberately do NOT fetch+blob here — let the
// browser stream the tar.gz directly with the server's
// Content-Disposition header.
export function downloadBackupURL(id: string): string {
  return `/api/v1/backups/${encodeURIComponent(id)}/download`
}

// Stage a restore. Backend returns 202 with {message} on success.
// Status-code taxonomy per the plan:
//   401 — wrong password
//   400 — archive validation failed
//   409 — a backup is currently running (rare on restore, but bubbled up)
//   413 — archive exceeds 5GB
//   507 — not enough disk space
// The calling component maps ApiClientError.status to its own UI copy;
// this function just surfaces whatever the client throws.
export async function stageRestore(
  file: File,
  password: string,
): Promise<{ message: string }> {
  const formData = new FormData()
  formData.append('archive', file)
  formData.append('password', password)
  return postMultipart<{ message: string }>('/backups/restore', formData)
}
