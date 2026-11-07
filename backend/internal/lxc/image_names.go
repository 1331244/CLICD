package lxc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"clicd/internal/config"
)

var imageCacheNameMu sync.Mutex

func imageArchiveCanonical(template Template) (string, error) {
	if template.Custom {
		return CustomImagePath(template.ID), nil
	}
	for _, component := range []string{template.Distro, template.Release, template.Arch} {
		if component == "" || component == "." || component == ".." || strings.ContainsAny(component, "/\\") {
			return "", fmt.Errorf("invalid template cache directory")
		}
	}
	directory := filepath.Join("/var/cache/lxc/download", template.Distro, template.Release, template.Arch)
	for _, canonical := range []string{filepath.Join(directory, "rootfs.tar.xz"), filepath.Join(directory, "default", "rootfs.tar.xz")} {
		if _, err := os.Lstat(canonical); err == nil {
			return canonical, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("cached rootfs archive not found")
}

// CachedImageFilename reports the actual regular-file basename, never its storage path.
func CachedImageFilename(template Template) string {
	canonical, err := imageArchiveCanonical(template)
	if err == nil {
		if actual, _, err := imageArchiveSource(canonical); err == nil {
			return filepath.Base(actual)
		}
	}
	return template.Filename
}

// RenameImageArchive keeps the native cache entry readable while changing its real file name.
func RenameImageArchive(template Template, name string) error {
	imageCacheNameMu.Lock()
	defer imageCacheNameMu.Unlock()
	return renameImageArchiveLocked(template, name)
}

func renameImageArchiveLocked(template Template, name string) error {
	canonical, err := imageArchiveCanonical(template)
	if err != nil {
		return err
	}
	// Native lxc-download can replace canonical with a fresh regular file. The
	// saved filename is owned by this image; stage that stale archive so the
	// transaction can publish the fresh bytes or restore the previous filename.
	var staged, stale string
	if info, statErr := os.Lstat(canonical); statErr == nil && info.Mode().IsRegular() {
		suffix, suffixErr := archiveSuffix(canonical)
		if suffixErr != nil {
			return suffixErr
		}
		filename, nameErr := imageArchiveBasename(name, suffix)
		if nameErr != nil {
			return nameErr
		}
		if filename == config.ImageCacheFilenames()[template.ID] && filename != filepath.Base(canonical) {
			stale = filepath.Join(filepath.Dir(canonical), filename)
			if old, staleErr := os.Lstat(stale); staleErr == nil {
				if !old.Mode().IsRegular() {
					return fmt.Errorf("previous image filename is not a regular file")
				}
				if !os.SameFile(old, info) {
					file, err := os.CreateTemp(filepath.Dir(canonical), ".clicd-image-old-*")
					if err != nil {
						return err
					}
					staged = file.Name()
					if err := file.Close(); err != nil {
						_ = os.Remove(staged)
						return err
					}
					_ = os.Remove(staged)
					if err := os.Rename(stale, staged); err != nil {
						return err
					}
				}
			} else if !os.IsNotExist(staleErr) {
				return staleErr
			}
		}
	}
	_, err = renameLXCArchiveFile(canonical, name, func(filename string) error {
		return config.SetImageNameAndFilename(template.ID, name, filename)
	})
	if staged != "" {
		if err != nil {
			if _, statErr := os.Lstat(stale); os.IsNotExist(statErr) {
				err = errors.Join(err, os.Rename(staged, stale))
			}
		} else {
			if cleanupErr := os.Remove(staged); cleanupErr != nil {
				log.Printf("image %s rename committed; stale archive cleanup failed: %v", template.ID, cleanupErr)
			}
		}
	}
	return err
}

// ApplyImageArchiveName restores a saved name after a native download replaces the cache entry.
func ApplyImageArchiveName(template Template) error {
	imageCacheNameMu.Lock()
	defer imageCacheNameMu.Unlock()
	name := config.ImageNameOverrides()[template.ID]
	if name == "" {
		return nil
	}
	return renameImageArchiveLocked(template, name)
}

func imageArchiveSource(canonical string) (string, os.FileInfo, error) {
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", nil, err
	}
	if info.Mode().IsRegular() {
		return canonical, info, nil
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", nil, fmt.Errorf("cached archive is not a regular file")
	}
	target, err := os.Readlink(canonical)
	if err != nil {
		return "", nil, err
	}
	if target == "" || target == "." || target == ".." || filepath.IsAbs(target) || filepath.Base(target) != target || strings.ContainsAny(target, "/\\") {
		return "", nil, fmt.Errorf("archive compatibility link must stay in its cache directory")
	}
	actual := filepath.Join(filepath.Dir(canonical), target)
	info, err = os.Lstat(actual)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("archive link target must be a regular file: %v", err)
	}
	return actual, info, nil
}

func archiveSuffix(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	header := make([]byte, 512)
	count, err := io.ReadFull(file, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	header = header[:count]
	for _, format := range []struct {
		magic  []byte
		suffix string
	}{
		{[]byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00}, ".tar.xz"},
		{[]byte{0x1f, 0x8b}, ".tar.gz"},
		{[]byte{0x28, 0xb5, 0x2f, 0xfd}, ".tar.zst"},
		{[]byte("BZh"), ".tar.bz2"},
	} {
		if bytes.HasPrefix(header, format.magic) {
			return format.suffix, nil
		}
	}
	if len(header) == 512 && (bytes.Equal(header, make([]byte, 512)) || bytes.Equal(header[257:262], []byte("ustar"))) {
		return ".tar", nil
	}
	return "", fmt.Errorf("unsupported cached rootfs archive format")
}

func imageArchiveBasename(name, suffix string) (string, error) {
	name, err := config.NormalizeImageDisplayName(name)
	if err != nil {
		return "", err
	}
	for _, extension := range []string{".tar.xz", ".tar.gz", ".tar.zst", ".tar.bz2", ".tar"} {
		if strings.HasSuffix(strings.ToLower(name), extension) {
			if extension != suffix {
				return "", fmt.Errorf("filename extension must match archive format %s", suffix)
			}
			return name, nil
		}
	}
	return name + suffix, nil
}

// replaceArchiveEntry stages an entry beside canonical before atomically replacing it.
func replaceArchiveEntry(canonical, source, target string) error {
	temporary, err := os.CreateTemp(filepath.Dir(canonical), ".clicd-image-name-*")
	if err != nil {
		return err
	}
	path := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	defer os.Remove(path)
	if err := os.Remove(path); err != nil {
		return err
	}
	if target != "" {
		err = os.Symlink(target, path)
	} else {
		err = os.Link(source, path)
	}
	if err != nil {
		return err
	}
	return os.Rename(path, canonical)
}

// The new name is first a hard link to the complete archive. Replacing canonical
// with a relative link is atomic, so consumers always see a readable archive.
func renameLXCArchiveFile(canonical, name string, publish func(filename string) error) (string, error) {
	actual, original, err := imageArchiveSource(canonical)
	if err != nil {
		return "", err
	}
	suffix, err := archiveSuffix(actual)
	if err != nil {
		return "", err
	}
	filename, err := imageArchiveBasename(name, suffix)
	if err != nil {
		return "", err
	}
	target := filepath.Join(filepath.Dir(canonical), filename)
	if target == actual {
		return filename, publish(filename)
	}
	created := false
	if target != canonical {
		if info, statErr := os.Lstat(target); statErr == nil {
			// A retry after interrupted metadata publication may find the new
			// hard link already present. Never replace unrelated existing bytes.
			if !info.Mode().IsRegular() || !os.SameFile(info, original) {
				return "", fmt.Errorf("target archive filename already exists")
			}
		} else if !os.IsNotExist(statErr) {
			return "", statErr
		} else if err := os.Link(actual, target); err != nil {
			return "", err
		} else {
			created = true
		}
	}
	link := filename
	if target == canonical {
		link = ""
	}
	if err := replaceArchiveEntry(canonical, actual, link); err != nil {
		if created {
			_ = os.Remove(target)
		}
		return "", err
	}
	if err := publish(filename); err != nil {
		previousLink := ""
		if actual != canonical {
			previousLink = filepath.Base(actual)
		}
		restoreErr := replaceArchiveEntry(canonical, target, previousLink)
		if restoreErr == nil && created {
			_ = os.Remove(target)
		}
		return "", errors.Join(err, restoreErr)
	}
	if actual != canonical && actual != target {
		if err := os.Remove(actual); err != nil {
			log.Printf("image archive rename committed; old filename cleanup failed: %v", err)
		}
	}
	return filename, nil
}
