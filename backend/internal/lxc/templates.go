package lxc

import (
	"runtime"
	"slices"
	"strings"

	"clicd/internal/config"
)

// Template represents an LXC image template
type Template struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Distro           string `json:"distro"`
	Release          string `json:"release"`
	Arch             string `json:"arch"`
	Variant          string `json:"variant"`
	Description      string `json:"description"`
	URL              string `json:"url,omitempty"`
	SHA256           string `json:"sha256,omitempty"`
	ArchiveSizeBytes int64  `json:"-"`
	Custom           bool   `json:"custom,omitempty"`
	Retired          bool   `json:"-"`
}

// DownloadsRootfsArchive selects the direct archive download path.
func (template Template) DownloadsRootfsArchive() bool {
	return template.Custom || template.URL != ""
}

// GetTemplates returns the templates currently offered for download.
func GetTemplates() []Template {
	return slices.DeleteFunc(KnownTemplates(), func(t Template) bool { return t.Retired })
}

// KnownTemplates also includes retired presets so existing image caches and
// saved container templates can still be resolved by ID.
func KnownTemplates() []Template {
	arch := defaultTemplateArch()
	templates := []Template{
		{
			ID: "ubuntu-noble", Name: "Ubuntu 24.04",
			Distro: "ubuntu", Release: "noble", Arch: arch,
			Description: "Ubuntu 24.04 LTS",
		},
		{
			ID: "ubuntu-jammy", Name: "Ubuntu 22.04",
			Distro: "ubuntu", Release: "jammy", Arch: arch,
			Description: "Ubuntu 22.04 LTS",
		},
		{
			ID: "debian-trixie", Name: "Debian 13",
			Distro: "debian", Release: "trixie", Arch: arch,
			Description: "Debian 13 (Trixie)",
		},
		{
			ID: "debian-bookworm", Name: "Debian 12",
			Distro: "debian", Release: "bookworm", Arch: arch,
			Description: "Debian 12 (Bookworm)",
		},
		{
			ID: "debian-bullseye", Name: "Debian 11",
			Distro: "debian", Release: "bullseye", Arch: arch,
			Description: "Debian 11 (Bullseye)",
		},
		{
			ID: "alpine-3.21", Name: "Alpine 3.21",
			Distro: "alpine", Release: "3.21", Arch: arch,
			Description: "Alpine Linux 3.21",
		},
		{
			ID: "centos-9-stream", Name: "CentOS 9 Stream",
			Distro: "centos", Release: "9-Stream", Arch: arch,
			Description: "CentOS 9 Stream",
		},
		{
			ID: "archlinux-current", Name: "Arch Linux",
			Distro: "archlinux", Release: "current", Arch: arch,
			Description: "Arch Linux (Rolling)",
		},
		{
			ID: "fedora-44", Name: "Fedora 44",
			Distro: "fedora", Release: "44", Arch: arch,
			Description: "Fedora 44",
		},
		{
			ID: "rockylinux-10", Name: "Rocky Linux 10",
			Distro: "rockylinux", Release: "10", Arch: arch,
			Description: "Rocky Linux 10",
		},
	}
	templates = append(templates, proxmoxAlpineTemplates(arch)...)
	for _, custom := range config.ListCustomLXCImages() {
		if custom.Arch != arch {
			continue
		}
		templates = append(templates, Template{
			ID:          custom.ID,
			Name:        custom.Name,
			Distro:      custom.Distro,
			Release:     custom.Release,
			Arch:        custom.Arch,
			Description: custom.Description,
			URL:         custom.URL,
			SHA256:      custom.SHA256,
			Custom:      true,
		})
	}
	names := config.ImageNameOverrides()
	for index := range templates {
		if name := names[templates[index].ID]; name != "" {
			templates[index].Name = name
		}
	}
	return templates
}

const proxmoxAlpineURL = "http://download.proxmox.com/images/system/"

func proxmoxAlpineTemplates(arch string) []Template {
	if arch != "amd64" {
		// Keep the former mirror's Alpine 3.24 cache resolvable on arm64.
		return []Template{{
			ID: "alpine-3.24", Name: "Alpine 3.24",
			Distro: "alpine", Release: "3.24", Arch: arch,
			Description: "Alpine Linux 3.24", Retired: true,
		}}
	}
	// The archives were checked against the SHA-512 values in Proxmox's
	// template indexes. Pin their SHA-256 values for the archive downloader.
	return []Template{
		{
			ID: "alpine-3.24", Name: "Alpine 3.24",
			Distro: "alpine", Release: "3.24", Arch: arch,
			Description:      "Alpine Linux 3.24",
			URL:              proxmoxAlpineURL + "alpine-3.24-default_20261105_amd64.tar.xz",
			SHA256:           "159becb595d518c2defabfb4e57d2bdbab792faea539a40a6c5f6c057f900df9",
			ArchiveSizeBytes: 3460064,
		},
		{
			ID: "alpine-3.23", Name: "Alpine 3.23",
			Distro: "alpine", Release: "3.23", Arch: arch,
			Description:      "Alpine Linux 3.23",
			URL:              proxmoxAlpineURL + "alpine-3.23-default_20261105_amd64.tar.xz",
			SHA256:           "5fbd1ec2349c981c9cd7e92e4eb493049a8573aa3fe24ddf353d72247715bf0b",
			ArchiveSizeBytes: 3450920,
		},
		{
			ID: "alpine-3.22", Name: "Alpine 3.22",
			Distro: "alpine", Release: "3.22", Arch: arch,
			Description:      "Alpine Linux 3.22",
			URL:              proxmoxAlpineURL + "alpine-3.22-default_20250617_amd64.tar.xz",
			SHA256:           "a578804161ae16ecddbd74034862c061403b87b8487c43c255eb5287bd2eac15",
			ArchiveSizeBytes: 3271244,
		},
	}
}

func defaultTemplateArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64"
	default:
		return "amd64"
	}
}

// FindTemplate finds a template by ID
func FindTemplate(id string) *Template {
	templates := KnownTemplates()
	for _, t := range templates {
		if t.ID == id {
			return &t
		}
	}
	return nil
}

// GetTemplateMinDiskGB returns the minimum recommended/required disk size in GB for an LXC template.
func GetTemplateMinDiskGB(templateID string) float64 {
	id := strings.ToLower(strings.TrimSpace(templateID))
	switch {
	case strings.Contains(id, "alpine"):
		return 0.5
	case strings.Contains(id, "debian"):
		return 1.0
	case strings.Contains(id, "ubuntu"):
		return 1.5
	case strings.Contains(id, "centos"), strings.Contains(id, "rocky"), strings.Contains(id, "fedora"), strings.Contains(id, "arch"):
		return 2.0
	default:
		return 0.5
	}
}
