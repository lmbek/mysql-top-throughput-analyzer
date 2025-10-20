package main

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RollingFileWriter is a simple, dependency-free size-based rotating writer.
// It writes to a primary file; when the size passes MaxSizeBytes, it rotates:
//   primary -> primary.YYYYMMDD-HHMMSS.N (N increments if file exists)
// It then opens a new primary file and continues writing.
// Retention: keeps at most MaxBackups files and deletes files older than MaxAge.
// Optional: Compress rotated files with gzip when Compress is true (best-effort).
//
// This is intentionally conservative and simple; it avoids races by serializing
// writes with a mutex. It is sufficient for single-process logging.
//
// Zero values for MaxBackups/MaxAgeDays mean "no limit" for that criterion.
// If MaxSizeBytes is zero, rotation is disabled and the file grows unbounded.

type RollingFileWriter struct {
	Path         string
	MaxSizeBytes int64
	MaxBackups   int
	MaxAgeDays   int
	Compress     bool

	mu   sync.Mutex
	f    *os.File
	size int64
}

func NewRollingFileWriter(path string, maxSizeMB int, maxBackups int, maxAgeDays int, compress bool) (*RollingFileWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	r := &RollingFileWriter{
		Path:         path,
		MaxSizeBytes: int64(maxSizeMB) * 1024 * 1024,
		MaxBackups:   maxBackups,
		MaxAgeDays:   maxAgeDays,
		Compress:     compress,
	}
	if err := r.openOrCreate(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RollingFileWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		if err := r.openOrCreate(); err != nil {
			return 0, err
		}
	}
	// Rotate if this write would exceed MaxSizeBytes (when enabled)
	if r.MaxSizeBytes > 0 && r.size+int64(len(p)) > r.MaxSizeBytes {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *RollingFileWriter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil {
		return r.f.Close()
	}
	return nil
}

func (r *RollingFileWriter) openOrCreate() error {
	f, err := os.OpenFile(r.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f = f
	r.size = fi.Size()
	return nil
}

func (r *RollingFileWriter) rotate() error {
	if r.f == nil {
		return errors.New("rotate: file not open")
	}
	// Close current file first
	if err := r.f.Close(); err != nil {
		return err
	}

	// Build rotated filename with timestamp and sequence suffix
	base := filepath.Base(r.Path)
	dir := filepath.Dir(r.Path)
	ts := time.Now().UTC().Format("20060102-150405")
	rot := filepath.Join(dir, fmt.Sprintf("%s.%s", base, ts))
	// Ensure uniqueness by incrementing .N if needed
	candidate := rot
	for i := 1; ; i++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			break
		}
		candidate = fmt.Sprintf("%s.%d", rot, i)
	}
	rot = candidate

	// Rename primary to rotated name
	if err := os.Rename(r.Path, rot); err != nil {
		// If rename fails (e.g., cross-device), attempt copy+truncate
		if err := copyFile(r.Path, rot); err != nil {
			// best effort: reopen and continue without rotation
			_ = r.openOrCreate()
			return err
		}
		// Truncate original
		_ = os.Truncate(r.Path, 0)
	}

	// Optionally compress rotated file
	if r.Compress {
		_ = gzipFile(rot)
	}

	// Reopen primary for new writes
	if err := r.openOrCreate(); err != nil {
		return err
	}

	// Enforce retention policies (best-effort)
	_ = r.cleanup()
	return nil
}

func (r *RollingFileWriter) cleanup() error {
	dir := filepath.Dir(r.Path)
	base := filepath.Base(r.Path)
	prefix := base + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type fileInfo struct {
		name string
		path string
		mod  time.Time
		size int64
	}
	var files []fileInfo
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		p := filepath.Join(dir, name)
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		files = append(files, fileInfo{name: name, path: p, mod: fi.ModTime(), size: fi.Size()})
	}
	// Sort by modtime descending (most recent first)
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })

	// Apply MaxBackups
	if r.MaxBackups > 0 && len(files) > r.MaxBackups {
		for _, f := range files[r.MaxBackups:] {
			_ = os.Remove(f.path)
		}
	}
	// Apply MaxAgeDays
	if r.MaxAgeDays > 0 {
		cut := time.Now().Add(-time.Duration(r.MaxAgeDays) * 24 * time.Hour)
		for _, f := range files {
			if f.mod.Before(cut) {
				_ = os.Remove(f.path)
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil { return err }
	defer s.Close()
	d, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil { return err }
	defer d.Close()
	if _, err := io.Copy(d, s); err != nil { return err }
	return nil
}

func gzipFile(path string) error {
	in, err := os.Open(path)
	if err != nil { return err }
	defer in.Close()
	out, err := os.OpenFile(path+".gz", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil { return err }
	gz := gzip.NewWriter(out)
	if _, err := io.Copy(gz, in); err != nil { _ = gz.Close(); _ = out.Close(); return err }
	if err := gz.Close(); err != nil { _ = out.Close(); return err }
	if err := out.Close(); err != nil { return err }
	// After successful compression, remove original
	_ = os.Remove(path)
	return nil
}
