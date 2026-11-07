package cli

import (
	"strings"
	"testing"
)

func TestReleaseArchiveAssetName(t *testing.T) {
	tests := map[string]string{
		"amd64": "clicd-linux-amd64.tar.gz",
		"arm64": "clicd-linux-arm64.tar.gz",
	}
	for goarch, want := range tests {
		got, err := releaseArchiveAssetName(goarch)
		if err != nil {
			t.Fatalf("releaseArchiveAssetName(%q) error = %v", goarch, err)
		}
		if got != want {
			t.Fatalf("releaseArchiveAssetName(%q) = %q, want %q", goarch, got, want)
		}
	}

	if _, err := releaseArchiveAssetName("386"); err == nil {
		t.Fatal("releaseArchiveAssetName(386) error = nil, want unsupported architecture")
	}
}

func TestFormatSSHAccessDoesNotExposePassword(t *testing.T) {
	out := formatSSHAccess(2222)
	if strings.Contains(out, "/") {
		t.Fatalf("formatSSHAccess output contains credential separator: %q", out)
	}
	if strings.Contains(strings.ToLower(out), "password123") {
		t.Fatalf("formatSSHAccess output exposed password: %q", out)
	}
	if !strings.Contains(out, "2222 -> 22") {
		t.Fatalf("formatSSHAccess output = %q, want SSH port mapping", out)
	}
}

func TestFormatSSHAccessHandlesMissingPort(t *testing.T) {
	out := formatSSHAccess(0)
	if !strings.Contains(out, "端口未分配") {
		t.Fatalf("formatSSHAccess output = %q, want missing port message", out)
	}
}
