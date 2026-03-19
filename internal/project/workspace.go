package project

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"agent-engine/internal/model"
)

func CopyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func BackupFiles(srcRoot, backupRoot string, paths []string) error {
	for _, rel := range uniquePaths(paths) {
		src := filepath.Join(srcRoot, rel)
		dst := filepath.Join(backupRoot, rel)
		info, err := os.Stat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.IsDir() {
			if err := CopyDir(src, dst); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func RestoreFiles(backupRoot, dstRoot string, paths []string) error {
	for _, rel := range uniquePaths(paths) {
		backup := filepath.Join(backupRoot, rel)
		target := filepath.Join(dstRoot, rel)
		info, err := os.Stat(backup)
		if err != nil {
			if os.IsNotExist(err) {
				if removeErr := os.RemoveAll(target); removeErr != nil {
					return removeErr
				}
				continue
			}
			return err
		}
		if info.IsDir() {
			if removeErr := os.RemoveAll(target); removeErr != nil {
				return removeErr
			}
			if err := CopyDir(backup, target); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := copyFile(backup, target); err != nil {
			return err
		}
	}
	return nil
}

func ApplyEdits(root string, edits []model.Edit) error {
	for _, edit := range edits {
		target := filepath.Join(root, filepath.Clean(edit.Path))
		switch strings.ToLower(edit.Action) {
		case "", "write", "modify", "replace":
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, []byte(edit.Content), 0o644); err != nil {
				return err
			}
		case "delete", "remove":
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return err
			}
		default:
			return fmt.Errorf("unsupported edit action %q", edit.Action)
		}
	}
	return nil
}

func SyncEdits(srcRoot, dstRoot string, edits []model.Edit) error {
	for _, edit := range edits {
		target := filepath.Join(dstRoot, filepath.Clean(edit.Path))
		source := filepath.Join(srcRoot, filepath.Clean(edit.Path))
		switch strings.ToLower(edit.Action) {
		case "", "write", "modify", "replace":
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := copyFile(source, target); err != nil {
				return err
			}
		case "delete", "remove":
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return err
			}
		default:
			return fmt.Errorf("unsupported edit action %q", edit.Action)
		}
	}
	return nil
}

func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func SensitivePath(rel string, sensitivePatterns []string) bool {
	cleaned := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(rel), "./"))
	for _, pattern := range sensitivePatterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		match, err := filepath.Match(pattern, filepath.Base(cleaned))
		if err == nil && match {
			return true
		}
		match, err = filepath.Match(pattern, cleaned)
		if err == nil && match {
			return true
		}
		if strings.Contains(cleaned, strings.Trim(pattern, "*")) {
			return true
		}
	}
	return false
}

func uniquePaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
