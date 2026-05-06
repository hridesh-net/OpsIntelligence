package dirs

import (
	"io"
	"os"
	"path/filepath"
)

// Migrate moves files and directories from the pre-v0.4 flat layout to the
// structured layout defined by Layout. It is idempotent: already-migrated
// entries are skipped. Errors on individual moves are logged to stderr but do
// not abort the migration so the process can still start.
//
// Old → New mappings:
//
//	<root>/ops.db                  → data/ops.db
//	<root>/memory/                 → data/memory/
//	<root>/mempalace/              → data/mempalace/
//	<root>/repointel/              → data/repointel/
//	<root>/localintel/             → data/localintel/
//	<root>/whatsapp.db             → data/whatsapp.db
//	<root>/pipeline-traces/        → logs/pipeline/
//	<root>/security/audit.ndjson   → logs/audit/audit.ndjson
//	<root>/opsintelligence.pid     → runtime/opsintelligence.pid
//	<root>/cron_jobs.json          → runtime/cron_jobs.json
//	<root>/channels/               → runtime/channels/
//	<root>/teams/                  → config/teams/
//	<root>/tools/                  → config/tools/
//	<root>/prompts/                → config/prompts/
func Migrate(l *Layout) {
	root := l.Root

	// Single-file moves: src → dst.
	fileMoves := [][2]string{
		{filepath.Join(root, "ops.db"), l.OpsDB()},
		{filepath.Join(root, "whatsapp.db"), l.WhatsAppDB()},
		{filepath.Join(root, "opsintelligence.pid"), l.PidFile()},
		{filepath.Join(root, "cron_jobs.json"), l.CronJobs()},
		{filepath.Join(root, "processes.json"), l.Processes()},
		{filepath.Join(root, "security", "audit.ndjson"), l.AuditLog()},
	}
	for _, pair := range fileMoves {
		migrateFile(pair[0], pair[1])
	}

	// Directory moves: contents of src are merged into dst.
	dirMoves := [][2]string{
		{filepath.Join(root, "memory"), l.Memory},
		{filepath.Join(root, "mempalace"), l.MemPalace},
		{filepath.Join(root, "repointel"), l.RepoIntel},
		{filepath.Join(root, "localintel"), l.LocalIntel},
		{filepath.Join(root, "pipeline-traces"), l.LogsPipeline},
		{filepath.Join(root, "channels"), l.Channels},
		{filepath.Join(root, "teams"), l.Teams},
		{filepath.Join(root, "tools"), l.Tools},
		{filepath.Join(root, "prompts"), l.Prompts},
	}
	for _, pair := range dirMoves {
		migrateDir(pair[0], pair[1])
	}

	// Remove legacy directories that have no equivalent in the new layout and
	// should be empty after migration. Errors are silently ignored — if a dir
	// still has contents it will not be removed.
	for _, legacy := range []string{
		filepath.Join(root, "policies"),  // superseded by root POLICIES.md
		filepath.Join(root, "datastore"), // never part of the canonical layout
	} {
		if entries, err := os.ReadDir(legacy); err == nil && len(entries) == 0 {
			os.Remove(legacy)
		}
	}
}

// migrateFile moves src to dst if src exists and dst does not.
func migrateFile(src, dst string) {
	if !fileExists(src) || fileExists(dst) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return
	}
	// Try atomic rename first; fall back to copy+delete for cross-device moves.
	if err := os.Rename(src, dst); err != nil {
		if copyFile(src, dst) == nil {
			os.Remove(src)
		}
	}
}

// migrateDir merges the contents of src into dst, then removes src if empty.
// When a destination entry already exists the source entry is removed (the
// destination is considered authoritative), so old flat directories are fully
// cleaned up even when the new structured directory was seeded first.
func migrateDir(src, dst string) {
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		return
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return
	}
	for _, e := range entries {
		srcEntry := filepath.Join(src, e.Name())
		dstEntry := filepath.Join(dst, e.Name())
		if fileExists(dstEntry) {
			// Destination already has this — clean up the source copy.
			if e.IsDir() {
				migrateDir(srcEntry, dstEntry) // recurse to clean sub-entries
			} else {
				os.Remove(srcEntry)
			}
			continue
		}
		if err := os.Rename(srcEntry, dstEntry); err != nil {
			if e.IsDir() {
				migrateDir(srcEntry, dstEntry)
			} else {
				if copyFile(srcEntry, dstEntry) == nil {
					os.Remove(srcEntry)
				}
			}
		}
	}
	// Remove now-empty source directory.
	if entries2, err := os.ReadDir(src); err == nil && len(entries2) == 0 {
		os.Remove(src)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
