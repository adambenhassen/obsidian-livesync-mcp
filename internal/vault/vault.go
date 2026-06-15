package vault

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// ErrPathEscape is returned when a note path resolves outside the vault root.
var ErrPathEscape = errors.New("note path escapes vault root")

// Vault provides filesystem CRUD over notes under a single root directory.
type Vault struct {
	root     string // absolute, cleaned (used to build/return note paths)
	realRoot string // root with all symlinks resolved (used for containment)
}

// New validates that root exists and is a directory, then returns a Vault.
func New(root string) (*Vault, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("vault root is not a directory")
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &Vault{root: abs, realRoot: realRoot}, nil
}

// resolve converts a vault-relative, forward-slashed note path into a safe
// absolute filesystem path, rejecting any path that escapes the root.
func (v *Vault) resolve(rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", ErrPathEscape
	}
	// Clean the relative path and reject any that traverses above the root.
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrPathEscape
	}
	abs := filepath.Join(v.root, clean)
	// Lexical containment: confirm the joined path is still under the root.
	check, err := filepath.Rel(v.root, abs)
	if err != nil || check == ".." || strings.HasPrefix(check, ".."+string(filepath.Separator)) {
		return "", ErrPathEscape
	}
	// Symlink-aware containment: a lexical check alone is bypassable by a
	// symlink inside the vault that points outside it. The vault is mutated by
	// an external sync process, so such symlinks are attacker-influenceable.
	// Resolve symlinks on the longest existing ancestor and confirm the real
	// target stays under the real root.
	realPath, err := evalExisting(abs)
	if err != nil {
		return "", err
	}
	if realPath != v.realRoot && !strings.HasPrefix(realPath, v.realRoot+string(filepath.Separator)) {
		return "", ErrPathEscape
	}
	return abs, nil
}

// evalExisting resolves symlinks on the longest existing prefix of p and
// re-appends the remaining (not-yet-created) suffix lexically. This lets a
// write target that does not exist yet still be checked for symlink escape via
// its existing parent directories.
func evalExisting(p string) (string, error) {
	var suffix []string
	cur := p
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			full := resolved
			for _, v := range slices.Backward(suffix) {
				full = filepath.Join(full, v)
			}
			return full, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err // reached filesystem root without an existing prefix
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
}

// ErrExists is returned by Write when overwrite is false and the note exists.
var ErrExists = errors.New("note already exists")

// Read returns the UTF-8 content of a note.
func (v *Vault) Read(rel string) (string, error) {
	abs, err := v.resolve(rel)
	if err != nil {
		return "", err
	}
	// abs is validated by resolve() against traversal and symlink escape.
	b, err := os.ReadFile(abs) //nolint:gosec // G304: path validated by resolve
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Write creates or updates a note. If overwrite is false and the note already
// exists, it returns ErrExists. Parent directories are created as needed.
func (v *Vault) Write(rel, content string, overwrite bool) error {
	abs, err := v.resolve(rel)
	if err != nil {
		return err
	}
	if !overwrite {
		if _, err := os.Stat(abs); err == nil {
			return ErrExists
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), 0o600)
}

// Append appends content to a note, creating it (and parents) if absent.
func (v *Vault) Append(rel, content string) error {
	abs, err := v.resolve(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return err
	}
	// abs is validated by resolve() against traversal and symlink escape.
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // G304: path validated by resolve
	if err != nil {
		return err
	}
	_, werr := f.WriteString(content)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

// Delete removes a note. Deletion propagates to CouchDB via the daemon's
// filesystem watcher as a LiveSync tombstone (verified by
// TestWriteNoteRoundtripToCouchDB).
func (v *Vault) Delete(rel string) error {
	abs, err := v.resolve(rel)
	if err != nil {
		return err
	}
	return os.Remove(abs)
}

// Move relocates a note, creating destination parents as needed. It refuses to
// overwrite an existing destination (returns ErrExists) so a rename can never
// silently destroy another note; callers that want a replace must delete first.
func (v *Vault) Move(from, to string) error {
	src, err := v.resolve(from)
	if err != nil {
		return err
	}
	dst, err := v.resolve(to)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return ErrExists
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// Note describes a single note's metadata. Path is vault-relative, slashed.
type Note struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

func (v *Vault) toNote(abs string, info os.FileInfo) (Note, error) {
	rel, err := filepath.Rel(v.root, abs)
	if err != nil {
		return Note{}, err
	}
	return Note{
		Path:    filepath.ToSlash(rel),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, nil
}

// List returns .md notes under folder. If recursive is false, only direct
// children are returned. folder "" means the vault root.
func (v *Vault) List(folder string, recursive bool) ([]Note, error) {
	base := v.root
	if folder != "" {
		abs, err := v.resolve(folder)
		if err != nil {
			return nil, err
		}
		base = abs
	}
	var notes []Note
	walk := func(abs string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if !recursive && abs != base {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(abs) != ".md" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		n, err := v.toNote(abs, info)
		if err != nil {
			return err
		}
		notes = append(notes, n)
		return nil
	}
	if err := filepath.WalkDir(base, walk); err != nil {
		return nil, err
	}
	return notes, nil
}

// Metadata returns metadata for a single note.
func (v *Vault) Metadata(rel string) (Note, error) {
	abs, err := v.resolve(rel)
	if err != nil {
		return Note{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Note{}, err
	}
	return v.toNote(abs, info)
}

// Search finds notes by mode "filename" (matches the path) or "content"
// (matches file body). Matching is case-insensitive substring.
func (v *Vault) Search(query, mode string) ([]Note, error) {
	if mode != "filename" && mode != "content" {
		return nil, errors.New("search mode must be \"filename\" or \"content\"")
	}
	all, err := v.List("", true)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	var matches []Note
	for _, n := range all {
		if mode == "filename" {
			if strings.Contains(strings.ToLower(n.Path), q) {
				matches = append(matches, n)
			}
			continue
		}
		body, err := v.Read(n.Path)
		if err != nil {
			return nil, err
		}
		if strings.Contains(strings.ToLower(body), q) {
			matches = append(matches, n)
		}
	}
	return matches, nil
}
