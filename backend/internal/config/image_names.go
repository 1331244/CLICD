package config

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// NormalizeImageDisplayName validates presentation metadata, never a cache path.
func NormalizeImageDisplayName(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("image name must be valid UTF-8")
	}
	for _, character := range value {
		if strings.ContainsRune(`<>:"/\|?*`, character) || unicode.IsControl(character) {
			return "", fmt.Errorf("image name must not contain filename separators, reserved characters or controls")
		}
	}
	if strings.HasSuffix(value, ".") || strings.HasSuffix(value, " ") {
		return "", fmt.Errorf("image name must not end with a dot or space")
	}
	name := strings.TrimSpace(value)
	if name == "" || name == "." || name == ".." || len(name) > 100 {
		return "", fmt.Errorf("image name must be between 1 and 100 UTF-8 bytes")
	}
	return name, nil
}

func copyImageNameOverrides(names map[string]string) map[string]string {
	result := make(map[string]string, len(names))
	for id, name := range names {
		result[id] = name
	}
	return result
}

// ImageNameOverrides returns a snapshot so catalog readers cannot mutate config.
func ImageNameOverrides() map[string]string {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	if AppConfig == nil {
		return nil
	}
	return copyImageNameOverrides(AppConfig.NameOverrides)
}

func ImageCacheFilenames() map[string]string {
	allocationMu.Lock()
	defer allocationMu.Unlock()
	if AppConfig == nil {
		return nil
	}
	return copyImageNameOverrides(AppConfig.ImageFiles)
}

// SetImageNameAndFilename atomically publishes display metadata without changing image identity.
// Callers must first verify that id identifies an existing LXC image.
func SetImageNameAndFilename(id, value, filename string) error {
	name, err := NormalizeImageDisplayName(value)
	if err != nil {
		return err
	}
	if id == "" || len(id) > 255 || strings.ContainsAny(id, "/\\\x00") {
		return fmt.Errorf("invalid LXC image ID")
	}
	if filename == "" || !utf8.ValidString(filename) || len(filename) > 255 || filename == "." || filename == ".." || strings.HasSuffix(filename, ".") || strings.HasSuffix(filename, " ") {
		return fmt.Errorf("invalid LXC image filename")
	}
	for _, character := range filename {
		if strings.ContainsRune(`<>:"/\|?*`, character) || unicode.IsControl(character) {
			return fmt.Errorf("invalid LXC image filename")
		}
	}
	allocationMu.Lock()
	defer allocationMu.Unlock()
	if AppConfig == nil {
		return fmt.Errorf("configuration is not loaded")
	}
	if AppConfig.NameOverrides[id] == name && AppConfig.ImageFiles[id] == filename {
		return nil
	}
	previous := AppConfig.NameOverrides
	previousFiles := AppConfig.ImageFiles
	updated := copyImageNameOverrides(previous)
	updatedFiles := copyImageNameOverrides(previousFiles)
	updated[id] = name
	updatedFiles[id] = filename
	AppConfig.NameOverrides = updated
	AppConfig.ImageFiles = updatedFiles
	if err := SaveConfig(); err != nil {
		AppConfig.NameOverrides = previous
		AppConfig.ImageFiles = previousFiles
		return err
	}
	return nil
}
