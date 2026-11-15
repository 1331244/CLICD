package lxc

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"clicd/internal/safehttp"
)

type CustomImageDownloadProgress struct {
	Stage           string
	DownloadedBytes int64
	TotalBytes      int64
	Percent         int
}

type CustomImageDownloadProgressFunc func(CustomImageDownloadProgress)

func RootfsArchivePath(id string) string {
	template := FindTemplate(id)
	if template == nil {
		return ""
	}
	return template.rootfsArchivePath()
}

func (template Template) rootfsArchivePath() string {
	const cache = "/var/cache/lxc/download"
	if template.Proxmox != nil {
		return filepath.Join(cache, "proxmox", template.Arch, template.ID, "rootfs.tar")
	}
	if template.Custom {
		return filepath.Join(cache, "custom", template.ID, "rootfs.tar")
	}
	return ""
}

func RootfsArchiveDownloadedInfo(id string) (bool, int64) {
	return rootfsArchiveInfo(RootfsArchivePath(id))
}

func rootfsArchiveInfo(archive string) (bool, int64) {
	info, err := os.Stat(archive)
	if err != nil || !info.Mode().IsRegular() {
		return false, 0
	}
	return true, info.Size()
}

// rootfsArchiveForInstall keeps native LXC caches usable across updates.
func (template Template) rootfsArchiveForInstall() string {
	archive := template.rootfsArchivePath()
	if template.Custom {
		return archive
	}
	if downloaded, _ := rootfsArchiveInfo(archive); downloaded {
		return archive
	}
	return ""
}

func DeleteRootfsArchive(id string) error {
	archive := RootfsArchivePath(id)
	if archive == "" {
		return fmt.Errorf("LXC rootfs archive is not managed by CLICD")
	}
	return os.RemoveAll(filepath.Dir(archive))
}

func DownloadRootfsArchiveWithProgress(ctx context.Context, template Template, progress CustomImageDownloadProgressFunc) error {
	target := RootfsArchivePath(template.ID)
	if target == "" {
		return fmt.Errorf("LXC rootfs archive is not managed by CLICD")
	}
	return downloadRootfsArchive(ctx, template, target, progress)
}

func downloadRootfsArchive(ctx context.Context, template Template, target string, progress CustomImageDownloadProgressFunc) error {
	if ok, _ := rootfsArchiveInfo(target); ok {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	tmp := target + ".tmp"
	_ = os.Remove(tmp)
	defer os.Remove(tmp)
	var expectedBytes int64
	if template.Proxmox != nil {
		expectedBytes = template.Proxmox.SizeBytes
	}
	if err := downloadRootfs(ctx, template.URL, tmp, expectedBytes, progress); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if template.Proxmox != nil {
		if err := verifyRootfsChecksum(tmp, sha512.New(), "SHA-512", template.Proxmox.SHA512); err != nil {
			return err
		}
	} else if template.SHA256 != "" {
		if err := verifyRootfsChecksum(tmp, sha256.New(), "SHA-256", template.SHA256); err != nil {
			return err
		}
	}
	if progress != nil {
		progress(CustomImageDownloadProgress{Stage: "validating", Percent: 100})
	}
	if err := ValidateCustomRootfsArchive(tmp); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func downloadRootfs(ctx context.Context, sourceURL, target string, expectedBytes int64, progress CustomImageDownloadProgressFunc) error {
	response, err := safehttp.Get(ctx, sourceURL, "CLICD/1.0 LXC image downloader", 30*time.Minute)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download failed: %s", response.Status)
	}
	total := response.ContentLength
	if expectedBytes > 0 {
		if total >= 0 && total != expectedBytes {
			return fmt.Errorf("rootfs download size mismatch: expected %d bytes, got %d", expectedBytes, total)
		}
		total = expectedBytes
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	buffer := make([]byte, 128*1024)
	var downloaded int64
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			if expectedBytes > 0 && downloaded+int64(count) > expectedBytes {
				return fmt.Errorf("rootfs download exceeds the expected %d bytes", expectedBytes)
			}
			if _, err := file.Write(buffer[:count]); err != nil {
				return err
			}
			downloaded += int64(count)
			if progress != nil {
				percent := 0
				if total > 0 {
					percent = int(downloaded * 100 / total)
					if percent > 100 {
						percent = 100
					}
				}
				progress(CustomImageDownloadProgress{
					Stage:           "downloading",
					DownloadedBytes: downloaded,
					TotalBytes:      total,
					Percent:         percent,
				})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if expectedBytes > 0 && downloaded != expectedBytes {
		return fmt.Errorf("rootfs download size mismatch: expected %d bytes, got %d", expectedBytes, downloaded)
	}
	return file.Sync()
}

func verifyRootfsChecksum(filePath string, digest hash.Hash, algorithm, expected string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(digest, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return fmt.Errorf("%s mismatch: expected %s, got %s", algorithm, expected, actual)
	}
	return nil
}

func ValidateCustomRootfsArchive(archivePath string) error {
	command := exec.Command("tar", "-tf", archivePath)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("failed to inspect rootfs archive: %v", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	entries := make([]string, 0, 4096)
	for scanner.Scan() {
		if len(entries) >= 2_000_000 {
			_ = command.Process.Kill()
			return fmt.Errorf("rootfs archive contains too many entries")
		}
		entries = append(entries, scanner.Text())
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	if scanErr != nil {
		return fmt.Errorf("failed to read rootfs archive: %v", scanErr)
	}
	if waitErr != nil {
		return fmt.Errorf("invalid rootfs archive: %v, output: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return validateCustomRootfsEntries(entries)
}

func validateCustomRootfsEntries(entries []string) error {
	hasInit := false
	for _, entry := range entries {
		entry = strings.TrimSpace(strings.ReplaceAll(entry, "\\", "/"))
		entry = strings.TrimPrefix(entry, "./")
		if entry == "" || entry == "." {
			continue
		}
		if strings.HasPrefix(entry, "/") {
			return fmt.Errorf("rootfs archive contains an absolute path: %s", entry)
		}
		clean := path.Clean(entry)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("rootfs archive contains path traversal: %s", entry)
		}
		switch strings.TrimSuffix(clean, "/") {
		case "sbin/init", "usr/lib/systemd/systemd", "lib/systemd/systemd", "bin/busybox", "bin/sh":
			hasInit = true
		}
	}
	if len(entries) == 0 {
		return fmt.Errorf("rootfs archive is empty")
	}
	if !hasInit {
		return fmt.Errorf("rootfs archive does not contain a supported init")
	}
	return nil
}

func extractRootfsArchive(archive, destination string) error {
	if ok, _ := rootfsArchiveInfo(archive); !ok {
		return fmt.Errorf("LXC rootfs image is not downloaded")
	}
	if err := ValidateCustomRootfsArchive(archive); err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0755); err != nil {
		return err
	}
	output, err := exec.Command("tar", "-xpf", archive, "-C", destination).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to extract custom LXC rootfs: %v, output: %s", err, strings.TrimSpace(string(output)))
	}
	if err := secureExtractedRootfs(destination); err != nil {
		return err
	}
	if !rootfsHasInit(destination) {
		return fmt.Errorf("extracted custom LXC rootfs is invalid: init not found")
	}
	return nil
}

func secureExtractedRootfs(root string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(filePath)
		if err != nil {
			return err
		}
		var resolved string
		if filepath.IsAbs(target) {
			resolved = filepath.Join(root, strings.TrimLeft(filepath.ToSlash(target), "/"))
			relative, err := filepath.Rel(filepath.Dir(filePath), resolved)
			if err != nil {
				return err
			}
			if err := os.Remove(filePath); err != nil {
				return err
			}
			if err := os.Symlink(relative, filePath); err != nil {
				return err
			}
		} else {
			resolved = filepath.Join(filepath.Dir(filePath), target)
		}
		relativeToRoot, err := filepath.Rel(root, filepath.Clean(resolved))
		if err != nil {
			return err
		}
		if relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("rootfs symlink escapes the archive root: %s -> %s", filePath, target)
		}
		return nil
	})
}
