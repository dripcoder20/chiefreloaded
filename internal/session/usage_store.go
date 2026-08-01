package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// This file persists the session's usage history to disk so general totals and
// past sessions survive a restart. It lives under the open project's .chief/
// directory, alongside the config and PRDs that are already local-only.
//
// Two properties matter:
//
//   - Writes are atomic. History is written to a temporary file and renamed over
//     the real one, so a process that dies mid-write leaves either the previous
//     valid file or the new one — never a truncated blend of the two.
//   - The format is versioned. A file written by an incompatible future version
//     is rejected as invalid rather than silently misread, so the load path can
//     surface a diagnosable error instead of corrupting the totals.

const (
	// usageFile is the history file's name inside .chief/.
	usageFile = "usage.json"
	// usageFileVersion is bumped when the on-disk shape changes incompatibly.
	usageFileVersion = 1
)

// usageStore reads and writes one project's usage history file.
type usageStore struct {
	path string
}

// newUsageStore targets the history file under a project root's .chief/.
func newUsageStore(root string) *usageStore {
	return &usageStore{path: filepath.Join(root, chiefDir, usageFile)}
}

// usageEnvelope is the on-disk shape: a version tag plus the attributed records.
// Records already carry every field needed to group them by run, PRD, story,
// attempt, provider and model, so persisting them verbatim is enough to rebuild
// the whole report on load.
type usageEnvelope struct {
	Version int           `json:"version"`
	Records []UsageRecord `json:"records"`
}

// load returns the persisted records, or an empty history when no file exists.
//
// A missing or empty file is not an error — it is the first-run case. A present
// file that cannot be parsed, or that carries an unsupported version, is an
// error: the caller surfaces it rather than silently discarding history.
func (s *usageStore) load() ([]UsageRecord, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read usage history %s: %w", s.path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}

	var env usageEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("parse usage history %s: %w", s.path, err)
	}
	if env.Version != usageFileVersion {
		return nil, fmt.Errorf(
			"usage history %s has unsupported version %d (expected %d)",
			s.path, env.Version, usageFileVersion)
	}
	return env.Records, nil
}

// save writes the records atomically, creating .chief/ if it does not yet exist.
func (s *usageStore) save(records []UsageRecord) error {
	data, err := json.MarshalIndent(usageEnvelope{Version: usageFileVersion, Records: records}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode usage history: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return writeFileAtomically(s.path, dir, data)
}

// writeFileAtomically writes data to a temporary file in dir and renames it over
// path. The temp file shares the destination's directory so the rename stays on
// one filesystem, which is what makes it atomic.
func writeFileAtomically(path, dir string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".usage-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp usage file: %w", err)
	}
	// A crash between here and the rename leaves this behind; a bail-out cleans it
	// up. After a successful rename the name no longer resolves and the removal is
	// a harmless no-op.
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp usage file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp usage file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp usage file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace usage history: %w", err)
	}
	return nil
}
