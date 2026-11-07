package lxc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func imageRenameTestTar(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	payload := []byte("fixture root filesystem bytes\n")
	if err := writer.WriteHeader(&tar.Header{Name: "etc/os-release", Mode: 0644, Size: int64(len(payload)), Format: tar.FormatUSTAR}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func imageRenameTestGzip(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(imageRenameTestTar(t)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func imageRenameTestSymlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("Windows symlink permission unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

func imageRenameTestRequireAtomicLinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("LXC archive publication requires Linux atomic symlink replacement semantics")
	}
}

func imageRenameTestWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func imageRenameTestRead(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("archive bytes changed at %s", path)
	}
}

func TestRenameLXCArchiveFileMovesBytesAndKeepsCanonicalAccess(t *testing.T) {
	imageRenameTestRequireAtomicLinks(t)
	dir := t.TempDir()
	canonical := filepath.Join(dir, "rootfs.tar")
	data := imageRenameTestGzip(t)
	imageRenameTestWrite(t, canonical, data)
	for _, name := range []string{"Alpine 3.24", "Alpine custom.tar.gz"} {
		called := false
		filename, err := renameLXCArchiveFile(canonical, name, func(filename string) error {
			called = true
			if filename == "" || filepath.Base(filename) != filename {
				t.Fatalf("published unsafe filename %q", filename)
			}
			imageRenameTestRead(t, canonical, data)
			imageRenameTestRead(t, filepath.Join(dir, filename), data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		want := name
		if !strings.HasSuffix(want, ".tar.gz") {
			want += ".tar.gz"
		}
		if !called || filename != want {
			t.Fatalf("result=%q, callback=%v, want %q", filename, called, want)
		}
		link, err := os.Readlink(canonical)
		if err != nil {
			t.Fatal(err)
		}
		if link != want {
			t.Fatalf("canonical link=%q, want %q", link, want)
		}
		info, err := os.Lstat(filepath.Join(dir, want))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("named archive must be a real file: %v, %v", info, err)
		}
		imageRenameTestRead(t, canonical, data)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("obsolete archive or stage files remain: %v", entries)
		}
	}
	// Repeating an already-applied name must preserve the same data and links.
	filename, err := renameLXCArchiveFile(canonical, "Alpine custom.tar.gz", func(string) error { return nil })
	if err != nil || filename != "Alpine custom.tar.gz" {
		t.Fatalf("repeat rename: %q, %v", filename, err)
	}
	imageRenameTestRead(t, canonical, data)
}

func TestRenameLXCArchiveFileDetectsActualFormat(t *testing.T) {
	imageRenameTestRequireAtomicLinks(t)
	for _, fixture := range []struct {
		name, suffix string
		data         []byte
	}{
		{"xz", ".tar.xz", append([]byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00}, []byte("fixture-xz-bytes")...)},
		{"gzip", ".tar.gz", imageRenameTestGzip(t)},
		{"zstd", ".tar.zst", append([]byte{0x28, 0xb5, 0x2f, 0xfd}, []byte("fixture-zstd-bytes")...)},
		{"bzip2", ".tar.bz2", append([]byte("BZh9"), []byte("fixture-bz2-bytes")...)},
		{"tar", ".tar", imageRenameTestTar(t)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			dir := t.TempDir()
			canonical := filepath.Join(dir, "rootfs.tar")
			imageRenameTestWrite(t, canonical, fixture.data)
			filename, err := renameLXCArchiveFile(canonical, "Chosen name", func(string) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if filename != "Chosen name"+fixture.suffix {
				t.Fatalf("filename=%q, want actual suffix %q", filename, fixture.suffix)
			}
			imageRenameTestRead(t, canonical, fixture.data)
			imageRenameTestRead(t, filepath.Join(dir, filename), fixture.data)
		})
	}
}

func TestRenameLXCArchiveFileCanUseNativeBasenameWithoutSelfLink(t *testing.T) {
	imageRenameTestRequireAtomicLinks(t)
	for _, alreadyNamed := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical already regular", true: "return from named file"}[alreadyNamed], func(t *testing.T) {
			dir := t.TempDir()
			canonical := filepath.Join(dir, "rootfs.tar.gz")
			data := imageRenameTestGzip(t)
			if alreadyNamed {
				imageRenameTestWrite(t, filepath.Join(dir, "Old name.tar.gz"), data)
				imageRenameTestSymlink(t, "Old name.tar.gz", canonical)
			} else {
				imageRenameTestWrite(t, canonical, data)
			}
			filename, err := renameLXCArchiveFile(canonical, "rootfs.tar.gz", func(filename string) error {
				imageRenameTestRead(t, canonical, data)
				return nil
			})
			if err != nil || filename != "rootfs.tar.gz" {
				t.Fatalf("native basename rename=%q, %v", filename, err)
			}
			info, err := os.Lstat(canonical)
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("native canonical should be regular, got %v, %v", info, err)
			}
			imageRenameTestRead(t, canonical, data)
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("obsolete named file remains: %v", entries)
			}
		})
	}
}

func TestRenameLXCArchiveFileRejectsConflictsAndWrongSuffix(t *testing.T) {
	for _, mode := range []string{"target file exists", "target link exists", "wrong compression suffix", "unknown format", "unsafe name"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			canonical := filepath.Join(dir, "rootfs.tar")
			data := imageRenameTestGzip(t)
			name := "Occupied"
			target := filepath.Join(dir, "Occupied.tar.gz")
			other := []byte("unrelated archive, must not change")
			switch mode {
			case "target file exists":
				imageRenameTestWrite(t, target, other)
			case "target link exists":
				outside := filepath.Join(t.TempDir(), "outside")
				imageRenameTestWrite(t, outside, other)
				imageRenameTestSymlink(t, outside, target)
			case "wrong compression suffix":
				name = "Wrong.tar.xz"
			case "unknown format":
				data = []byte("not a root filesystem archive")
			case "unsafe name":
				name = "../escape"
			}
			imageRenameTestWrite(t, canonical, data)
			called := false
			if _, err := renameLXCArchiveFile(canonical, name, func(string) error { called = true; return nil }); err == nil {
				t.Fatal("invalid rename succeeded")
			}
			if called {
				t.Fatal("invalid rename attempted to publish metadata")
			}
			info, err := os.Lstat(canonical)
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("original canonical regular file was changed: %v", err)
			}
			imageRenameTestRead(t, canonical, data)
			if mode == "target file exists" || mode == "target link exists" {
				imageRenameTestRead(t, target, other)
			}
		})
	}
}

func TestRenameLXCArchiveFileRejectsUnsafeCanonicalLinks(t *testing.T) {
	for _, mode := range []string{"absolute", "escape", "chain"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			canonical := filepath.Join(dir, "rootfs.tar")
			data := imageRenameTestGzip(t)
			outside := filepath.Join(t.TempDir(), "original.tar.gz")
			imageRenameTestWrite(t, outside, data)
			target := outside
			if mode == "escape" {
				var err error
				target, err = filepath.Rel(dir, outside)
				if err != nil {
					t.Fatal(err)
				}
			} else if mode == "chain" {
				real := filepath.Join(dir, "actual.tar.gz")
				imageRenameTestWrite(t, real, data)
				imageRenameTestSymlink(t, "actual.tar.gz", filepath.Join(dir, "chain.tar.gz"))
				target = "chain.tar.gz"
			}
			imageRenameTestSymlink(t, target, canonical)
			called := false
			if _, err := renameLXCArchiveFile(canonical, "New name", func(string) error { called = true; return nil }); err == nil {
				t.Fatal("unsafe canonical link accepted")
			}
			if called {
				t.Fatal("unsafe link attempted metadata publication")
			}
			link, err := os.Readlink(canonical)
			if err != nil || link != target {
				t.Fatalf("original link changed: %q, %v", link, err)
			}
			imageRenameTestRead(t, canonical, data)
			imageRenameTestRead(t, outside, data)
		})
	}
}

func TestRenameLXCArchiveFileRollsBackPersistenceFailure(t *testing.T) {
	imageRenameTestRequireAtomicLinks(t)
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "original regular file", true: "already renamed archive"}[named], func(t *testing.T) {
			dir := t.TempDir()
			canonical := filepath.Join(dir, "rootfs.tar")
			data := imageRenameTestGzip(t)
			oldFilename := "Old name.tar.gz"
			if named {
				imageRenameTestWrite(t, filepath.Join(dir, oldFilename), data)
				imageRenameTestSymlink(t, oldFilename, canonical)
			} else {
				imageRenameTestWrite(t, canonical, data)
			}
			failure := errors.New("fixture persistence failure")
			_, err := renameLXCArchiveFile(canonical, "New name", func(string) error {
				imageRenameTestRead(t, canonical, data)
				return failure
			})
			if !errors.Is(err, failure) {
				t.Fatalf("error=%v, want original persistence failure", err)
			}
			imageRenameTestRead(t, canonical, data)
			if _, err := os.Lstat(filepath.Join(dir, "New name.tar.gz")); !os.IsNotExist(err) {
				t.Fatalf("failed rename left new file: %v", err)
			}
			if named {
				link, err := os.Readlink(canonical)
				if err != nil || link != oldFilename {
					t.Fatalf("old canonical link not restored: %q, %v", link, err)
				}
				imageRenameTestRead(t, filepath.Join(dir, oldFilename), data)
			} else {
				info, err := os.Lstat(canonical)
				if err != nil || !info.Mode().IsRegular() {
					t.Fatalf("old regular canonical not restored: %v", err)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			if named {
				wantCount = 2
			}
			if len(entries) != wantCount {
				t.Fatalf("failed rename left stage files: %v", entries)
			}
		})
	}
}
