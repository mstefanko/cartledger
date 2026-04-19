package spreadsheet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Staging is the persistent per-import session. It holds the raw file path
// (on disk), every sheet's ParsedSheet (already parsed once at upload), the
// transform chain, and a few bookkeeping timestamps. Staging is serialized
// to staging.json alongside the raw file under one directory per import_id.
type Staging struct {
	ImportID     string                  `json:"import_id"`
	RawPath      string                  `json:"raw_path"`
	Parsed       map[string]*ParsedSheet `json:"parsed"`
	Chain        TransformChain          `json:"chain"`
	CreatedAt    time.Time               `json:"created_at"`
	LastActiveAt time.Time               `json:"last_active_at"`
}

// ApplyTransforms returns a new ParsedSheet with the chain's transforms
// applied in order. The input sheet is not mutated; rows are shallow-copied
// and any edited row's cells slice is duplicated before mutation.
//
// v1 kinds implemented:
//   - KindOverrideCell: set sheet.Rows[row_index].Cells[col_index] = new_value
//     (expands the cells slice if col_index is past its current width).
//   - KindSkipRow: remove the referenced rows from the output.
//
// Phase 10 kinds (KindAINormalize, KindSplitRow) are accepted and skipped —
// calling ApplyTransforms on a chain that contains them is a no-op on those
// entries, not an error, so mixed-kind chains remain valid during rollout.
func ApplyTransforms(parsed *ParsedSheet, chain TransformChain) *ParsedSheet {
	if parsed == nil {
		return nil
	}

	// Start with a shallow copy of the sheet metadata and a deep-enough copy
	// of rows (cells slices are copied only when modified — see override loop).
	out := &ParsedSheet{
		Name:          parsed.Name,
		Headers:       parsed.Headers,
		Rows:          make([]RawRow, len(parsed.Rows)),
		ColumnSamples: parsed.ColumnSamples,
		TypeCoverage:  parsed.TypeCoverage,
	}
	copy(out.Rows, parsed.Rows)

	// Collect the set of skipped row indices so we can filter at the end in
	// one pass, preserving ordering and avoiding O(n²) slice splicing.
	skipped := map[int]bool{}

	// Build an index so override transforms can find their target row in
	// O(1). RawRow.Index is the authoritative row key; ApplyTransforms
	// refuses to edit by slice position because skip/split may have shifted
	// row positions in earlier transforms.
	indexByRowKey := make(map[int]int, len(out.Rows))
	for i, r := range out.Rows {
		indexByRowKey[r.Index] = i
	}

	for _, t := range chain.Transforms {
		switch t.Kind {
		case KindOverrideCell:
			var p OverrideCellPayload
			if err := json.Unmarshal(t.Payload, &p); err != nil {
				continue // silently skip malformed — preview layer surfaces this separately
			}
			i, ok := indexByRowKey[p.RowIndex]
			if !ok {
				continue
			}
			// Copy-on-write the Cells slice before mutating.
			newCells := make([]string, max(len(out.Rows[i].Cells), p.ColIndex+1))
			copy(newCells, out.Rows[i].Cells)
			if p.ColIndex >= 0 && p.ColIndex < len(newCells) {
				newCells[p.ColIndex] = p.NewValue
			}
			out.Rows[i].Cells = newCells

		case KindSkipRow:
			var p SkipRowPayload
			if err := json.Unmarshal(t.Payload, &p); err != nil {
				continue
			}
			for _, ri := range p.RowIndices {
				skipped[ri] = true
			}

		case KindAINormalize, KindSplitRow:
			// TODO(phase10): implement structural AI-driven transforms.
			continue
		}
	}

	if len(skipped) > 0 {
		kept := out.Rows[:0]
		for _, r := range out.Rows {
			if skipped[r.Index] {
				continue
			}
			kept = append(kept, r)
		}
		out.Rows = kept
	}
	return out
}

// SaveStaging writes s to `{basePath}/{importID}/staging.json`. The raw
// source file is expected to live alongside as `raw.<ext>` — SaveStaging
// does not manage raw-file I/O; it only persists the staging metadata.
func SaveStaging(basePath string, s *Staging) error {
	if s == nil {
		return fmt.Errorf("nil staging")
	}
	dir := filepath.Join(basePath, s.ImportID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir staging: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal staging: %w", err)
	}
	tmp := filepath.Join(dir, "staging.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write staging: %w", err)
	}
	// Atomic rename so partial writes never replace a good staging.json.
	if err := os.Rename(tmp, filepath.Join(dir, "staging.json")); err != nil {
		return fmt.Errorf("rename staging: %w", err)
	}
	return nil
}

// LoadStaging reads and unmarshals `{basePath}/{importID}/staging.json`.
func LoadStaging(basePath, importID string) (*Staging, error) {
	path := filepath.Join(basePath, importID, "staging.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read staging: %w", err)
	}
	var s Staging
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal staging: %w", err)
	}
	return &s, nil
}

// DeleteStaging removes the staging directory for importID (raw file,
// staging.json, anything else the handler put there). Used for cancel and
// janitor cleanup.
func DeleteStaging(basePath, importID string) error {
	dir := filepath.Join(basePath, importID)
	return os.RemoveAll(dir)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
