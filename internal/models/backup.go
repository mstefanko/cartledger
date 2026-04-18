package models

import "time"

// Backup is the row shape of the `backups` table. Rows are created when a
// backup starts (status='running') and updated to 'complete' or 'failed'
// when it finishes. Error is non-nil only on 'failed' rows; SizeBytes and
// CompletedAt are non-nil only on 'complete' rows.
type Backup struct {
	ID             string     `json:"id"`
	Status         string     `json:"status"`
	Filename       string     `json:"filename"`
	SizeBytes      *int64     `json:"size_bytes,omitempty"`
	SchemaVersion  int        `json:"schema_version"`
	MissingImages  int        `json:"missing_images"`
	Error          *string    `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// Backup status constants.
const (
	BackupStatusRunning  = "running"
	BackupStatusComplete = "complete"
	BackupStatusFailed   = "failed"
)
