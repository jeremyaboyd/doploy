// Package archive packs a local directory for upload to a droplet.
package archive

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// zeroTime normalises archive timestamps so an unchanged context produces
// byte-identical output.
var zeroTime = time.Unix(0, 0).UTC()

// MaxContextBytes caps an uploaded build context.
//
// The context travels base64-encoded through an SSH exec channel, so a
// forgotten node_modules would otherwise turn a deploy into a very slow
// mystery. Failing loudly at 64 MiB is kinder than uploading it.
const MaxContextBytes = 64 << 20

// alwaysIgnored is skipped regardless of .dockerignore. These are never wanted
// in an image and are the usual cause of an accidentally enormous context.
var alwaysIgnored = []string{".git", ".hg", ".svn", "node_modules", ".DS_Store", ".terraform"}

// TarGz packs dir into a gzipped tar suitable for extraction on a droplet.
//
// Paths in the archive are relative to dir. A .dockerignore in dir is honoured
// with simplified matching: exact names, path.Match globs, and directory
// prefixes. It does not implement Docker's full negation syntax.
func TarGz(dir string) ([]byte, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("reading build context %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("build context %s is not a directory", dir)
	}

	ignore, err := loadIgnore(dir)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	var total int64

	walkErr := filepath.WalkDir(dir, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == dir {
			return nil
		}

		rel, err := filepath.Rel(dir, current)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if ignore.matches(rel, entry.IsDir()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		// Symlinks and devices are skipped rather than followed: following them
		// can escape the context entirely.
		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel
		// Zero the timestamps so an unchanged context produces an identical
		// archive, which keeps Docker's layer cache warm across deploys.
		header.ModTime = zeroTime
		header.AccessTime = zeroTime
		header.ChangeTime = zeroTime
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""

		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		total += info.Size()
		if total > MaxContextBytes {
			return fmt.Errorf("build context %s exceeds %d MiB; add a .dockerignore to exclude what the image does not need",
				dir, MaxContextBytes>>20)
		}

		file, err := os.Open(current)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tw, file)
		return err
	})
	if walkErr != nil {
		return nil, walkErr
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ignoreList is a set of simplified .dockerignore patterns.
type ignoreList []string

func loadIgnore(dir string) (ignoreList, error) {
	list := append(ignoreList{}, alwaysIgnored...)

	file, err := os.Open(filepath.Join(dir, ".dockerignore"))
	if os.IsNotExist(err) {
		return list, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Negation is not supported; skipping it is safer than mis-including.
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		list = append(list, strings.TrimSuffix(line, "/"))
	}
	return list, scanner.Err()
}

// matches reports whether a context-relative path should be excluded.
func (ig ignoreList) matches(rel string, isDir bool) bool {
	base := path.Base(rel)

	for _, pattern := range ig {
		if pattern == "" {
			continue
		}
		if ok, _ := path.Match(pattern, rel); ok {
			return true
		}
		if ok, _ := path.Match(pattern, base); ok {
			return true
		}
		// A directory pattern excludes everything beneath it.
		if strings.HasPrefix(rel, pattern+"/") {
			return true
		}
	}
	return false
}
