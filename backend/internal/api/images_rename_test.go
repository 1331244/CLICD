package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"clicd/internal/config"
	"clicd/internal/kvm"
	"clicd/internal/lxc"
)

// Keep the SQLite store and mutable download state isolated from the other API
// tests. The downloaded precondition is simulated; no real image cache is used.
func TestImageRenameContract(t *testing.T) {
	const fixtureEnv = "CLICD_IMAGE_RENAME_TEST_DIR"
	dir := os.Getenv(fixtureEnv)
	if dir == "" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(executable, "-test.run=^TestImageRenameContract$", "-test.v")
		cmd.Env = append(os.Environ(), fixtureEnv+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated rename fixture: %v\n%s", err, output)
		} else {
			t.Log(string(output))
		}
		return
	}
	customID := "custom-lxc-rename-test"
	custom := config.CustomLXCImage{
		ID: customID, Name: "Original custom name", Distro: "alpine", Release: "3.24", Arch: runtime.GOARCH,
		URL: "https://example.invalid/alpine-rootfs.tar.xz", SHA256: strings.Repeat("b", 64), CreatedAt: "2026-11-07 00:00:00",
	}
	fixture := config.ClicdConfig{
		AdminUser: "rename-fixture", AdminPassHash: "fixture-hash", JWTSecret: "fixture-secret", Port: 8999, DataDir: dir,
		CustomLXCImages: []config.CustomLXCImage{custom}, EnabledImages: []string{customID, "alpine-3.21"},
		Containers: []config.Container{{ID: 77, UUID: "rename-bound-container", Name: "bound", Template: customID, Status: "stopped"}},
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	config.SetConfigPath(path)
	if _, err := config.InitConfig(); err != nil {
		t.Fatal(err)
	}
	previousProbe := renameLXCImageDownloadedInfo
	defer func() { renameLXCImageDownloadedInfo = previousProbe }()
	previousRename := renameLXCImageArchive
	defer func() { renameLXCImageArchive = previousRename }()
	renameLXCImageArchive = func(template lxc.Template, name string) error {
		return config.SetImageNameAndFilename(template.ID, name, name+".tar.xz")
	}
	ready := map[string]bool{customID: true, "alpine-3.21": true}
	renameLXCImageDownloadedInfo = func(template lxc.Template) (bool, int64) { return ready[template.ID], 1234 }

	request := func(method string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		HandleImageRename(rec, httptest.NewRequest(method, "/api/v1/images/rename", bytes.NewReader(body)))
		return rec
	}
	rename := func(id, name string) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"template_id": id, "name": name})
		if err != nil {
			t.Fatal(err)
		}
		return request(http.MethodPut, payload)
	}
	expectStatus := func(rec *httptest.ResponseRecorder, want int) {
		t.Helper()
		if rec.Code != want {
			t.Fatalf("status=%d, want %d: %s", rec.Code, want, rec.Body.String())
		}
	}

	for _, invalid := range []struct {
		name string
		body []byte
	}{
		{"malformed JSON", []byte("{")},
		{"unknown field", []byte(`{"template_id":"` + customID + `","name":"New","url":"https://other.invalid"}`)},
		{"trailing JSON", []byte(`{"template_id":"` + customID + `","name":"New"}{}`)},
		{"oversized request", []byte(`{"template_id":"` + customID + `","name":"` + strings.Repeat("n", 17*1024) + `"}`)},
		{"invalid UTF-8", append(append([]byte(`{"template_id":"`+customID+`","name":"`), 0xff), []byte(`"}`)...)},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			rec := request(http.MethodPut, invalid.body)
			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("status=%d, want client error: %s", rec.Code, rec.Body.String())
			}
			if got := lxc.FindTemplate(customID).Name; got != custom.Name {
				t.Fatalf("invalid input changed name to %q", got)
			}
		})
	}
	for _, name := range []string{"", "   ", "line\nbreak", "tab\tname", "\ttrimmed", "name\u007f", "name\u0085", "pipe|name", "path/name", `path\name`, `colon:name`, `question?`, `star*name`, `quote"name`, `angle<name`, `angle>name`, "ending.", "ending ", strings.Repeat("a", 101), strings.Repeat("镜", 34)} {
		t.Run("invalid name "+name, func(t *testing.T) {
			expectStatus(rename(customID, name), http.StatusBadRequest)
			if got := lxc.FindTemplate(customID).Name; got != custom.Name {
				t.Fatalf("invalid name changed metadata to %q", got)
			}
		})
	}
	expectStatus(request(http.MethodGet, nil), http.StatusMethodNotAllowed)
	expectStatus(rename("missing-lxc", "New"), http.StatusNotFound)
	expectStatus(rename(kvm.GetImages()[0].ID, "New KVM"), http.StatusNotFound)
	expectStatus(rename("debian-trixie", "Not downloaded"), http.StatusConflict)
	if got := lxc.FindTemplate("debian-trixie").Name; got != "Debian 13" {
		t.Fatalf("undownloaded template changed to %q", got)
	}
	if _, ok := startImageDownload(customID, "queued"); !ok {
		t.Fatal("could not stage busy download")
	}
	expectStatus(rename(customID, "Busy rename"), http.StatusConflict)
	clearImageDownload(customID)
	for _, task := range []config.SavedTask{
		{ID: "pending-create", Type: "create", Status: "pending", Config: `{"template_id":"` + customID + `"}`},
		{ID: "running-reinstall", Type: "reinstall", Status: "running", TemplateID: customID},
	} {
		t.Run("saved task "+task.ID, func(t *testing.T) {
			before := *lxc.FindTemplate(customID)
			config.AppConfig.Tasks = []config.SavedTask{task}
			expectStatus(rename(customID, "Blocked by active task"), http.StatusConflict)
			if after := *lxc.FindTemplate(customID); !reflect.DeepEqual(before, after) {
				t.Fatalf("active task rename changed metadata: before=%+v after=%+v", before, after)
			}
		})
	}
	config.AppConfig.Tasks = []config.SavedTask{
		{ID: "done-create", Type: "create", Status: "done", Config: `{"template_id":"` + customID + `"}`},
		{ID: "done-reinstall", Type: "reinstall", Status: "done", TemplateID: customID},
	}

	beforeCustom := *lxc.FindTemplate(customID)
	customCache := lxc.CustomImagePath(customID)
	builtinCache, _ := officialLXCImageCachePath("alpine-3.21")
	boundID := config.AppConfig.Containers[0].Template
	enabled := append([]string(nil), config.AppConfig.EnabledImages...)
	expectStatus(rename(customID, "Renamed Alpine 3.24"), http.StatusOK)
	afterCustom := *lxc.FindTemplate(customID)
	if afterCustom.Name != "Renamed Alpine 3.24" {
		t.Fatalf("custom name=%q", afterCustom.Name)
	}
	if afterCustom.Filename != "Renamed Alpine 3.24.tar.xz" {
		t.Fatalf("custom filename=%q", afterCustom.Filename)
	}
	afterCustom.Name = beforeCustom.Name
	afterCustom.Filename = beforeCustom.Filename
	if !reflect.DeepEqual(beforeCustom, afterCustom) {
		t.Fatalf("rename modified source metadata: before=%+v after=%+v", beforeCustom, afterCustom)
	}
	if lxc.CustomImagePath(customID) != customCache {
		t.Fatal("custom cache path changed")
	}
	if config.AppConfig.Containers[0].Template != boundID || !reflect.DeepEqual(enabled, config.AppConfig.EnabledImages) {
		t.Fatal("rename changed bindings or enablement")
	}
	expectStatus(rename(customID, "Renamed Alpine 3.24"), http.StatusOK)
	expectStatus(rename("alpine-3.21", "renamed alpine 3.24"), http.StatusConflict)

	maxName := strings.Repeat("镜", 33) + "A" // Exactly 100 UTF-8 bytes.
	expectStatus(rename("alpine-3.21", maxName), http.StatusOK)
	if got := lxc.FindTemplate("alpine-3.21"); got.ID != "alpine-3.21" || got.Name != maxName || got.Filename != maxName+".tar.xz" || got.Distro != "alpine" || got.Release != "3.21" {
		t.Fatalf("built-in identity changed: %+v", got)
	}
	if path, _ := officialLXCImageCachePath("alpine-3.21"); path != builtinCache {
		t.Fatal("built-in cache path changed")
	}

	for _, endpoint := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/api/v1/images", HandleImages}, {"/api/v1/templates", HandleTemplates},
	} {
		rec := httptest.NewRecorder()
		endpoint.handler(rec, httptest.NewRequest(http.MethodGet, endpoint.path, nil))
		expectStatus(rec, http.StatusOK)
		var response struct {
			Success bool `json:"success"`
			Data    []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, image := range response.Data {
			found[image.ID] = image.Name
		}
		if !response.Success || found[customID] != "Renamed Alpine 3.24" || found["alpine-3.21"] != maxName {
			t.Fatalf("%s returned inconsistent names: %s", endpoint.path, rec.Body.String())
		}
	}
}
