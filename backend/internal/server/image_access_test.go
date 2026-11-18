package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"clicd/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

func TestImageManagementRoutesRequireAdministratorAndOperationScope(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.ClicdConfig{
		JWTSecret: "image-permission-test",
		SubUsers:  []config.SubUser{{Username: "tenant"}},
		ApiKeys: []config.ApiKeyConfig{{
			ID: "limited-images",
			// Legacy fixture is migrated by authentication on its first use.
			KeyHash: "636c6963645f736b5f73656375726974795f626f756e646172795f7465737400",
		}},
	}
	mux := http.NewServeMux()
	setupRoutes(mux)
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/images/custom"},
		{http.MethodPost, "/api/v1/images/download"},
		{http.MethodPost, "/api/v1/images/cancel"},
		{http.MethodDelete, "/api/v1/images/delete"},
		{http.MethodPut, "/api/v1/images/toggle"},
	} {
		for _, access := range []struct {
			name, token string
			scopes      []string
			want        int
		}{
			{"image scope without administrator", "", []string{"image:*"}, http.StatusForbidden},
			{"administrator without image scope", "", []string{"admin:access"}, http.StatusForbidden},
			{"administrator with image scope", "", []string{"admin:access", "image:*"}, http.StatusBadRequest},
			{"administrator session", imageAccessToken(t, ""), nil, http.StatusBadRequest},
			{"tenant session", imageAccessToken(t, "tenant"), nil, http.StatusForbidden},
		} {
			t.Run(route.path+"/"+access.name, func(t *testing.T) {
				config.AppConfig.ApiKeys[0].Scopes = access.scopes
				req := httptest.NewRequest(route.method, route.path, strings.NewReader("{"))
				if access.token != "" {
					req.Header.Set("Authorization", "Bearer "+access.token)
				} else {
					req.Header.Set("X-API-Key", "clicd_sk_security_boundary_test")
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != access.want {
					t.Fatalf("status=%d, want %d: %s", rec.Code, access.want, rec.Body.String())
				}
			})
		}
	}
	config.AppConfig.ApiKeys[0].Scopes = []string{"image:read"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/images/enabled", nil)
	req.Header.Set("X-API-Key", "clicd_sk_security_boundary_test")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("read-only image access status=%d: %s", rec.Code, rec.Body.String())
	}
}

func TestImageRenameRoutesAreRetired(t *testing.T) {
	mux := http.NewServeMux()
	setupRoutes(mux)
	for _, path := range []string{"/api/images/rename", "/api/v1/images/rename"} {
		req := httptest.NewRequest(http.MethodPut, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s returned %d, want 404", path, rec.Code)
		}
	}
}

func TestLXCDownloadCatalogIncludesOnlyRequestedProxmoxPresets(t *testing.T) {
	previous := config.AppConfig
	t.Cleanup(func() { config.AppConfig = previous })
	config.AppConfig = &config.ClicdConfig{JWTSecret: "native-image-catalog-test"}
	mux := http.NewServeMux()
	setupRoutes(mux)
	authorization := "Bearer " + imageAccessToken(t, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/templates", nil)
	req.Header.Set("Authorization", authorization)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	type templateEntry struct {
		ID, URL string
		Custom  bool
	}
	var response struct{ Data []templateEntry }
	want := map[string]string{}
	if runtime.GOARCH == "amd64" {
		want = map[string]string{
			"alpine-3.24": "alpine-3.24-default_20261105_amd64.tar.xz",
			"alpine-3.23": "alpine-3.23-default_20261105_amd64.tar.xz",
			"alpine-3.22": "alpine-3.22-default_20250617_amd64.tar.xz",
		}
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil ||
		len(response.Data) != 10+len(want) || response.Data[0].ID != "ubuntu-noble" || response.Data[5].ID != "alpine-3.21" {
		t.Fatalf("unexpected template catalog: %d %s", rec.Code, rec.Body.String())
	}

	for _, template := range response.Data {
		url := ""
		if file, ok := want[template.ID]; ok {
			url = "http://download.proxmox.com/images/system/" + file
			delete(want, template.ID)
		}
		if template.Custom || template.URL != url {
			t.Fatalf("unexpected source for template: %+v", template)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing Proxmox presets: %v", want)
	}
	if runtime.GOARCH == "amd64" {
		req = httptest.NewRequest(http.MethodGet, "/api/v1/images", nil)
		req.Header.Set("Authorization", authorization)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var imageCatalog struct {
			Data []struct {
				ID, Type, Arch string
				Custom         bool
				SizeBytes      int64 `json:"size_bytes"`
			}
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &imageCatalog) != nil {
			t.Fatalf("unexpected image catalog: %d %s", rec.Code, rec.Body.String())
		}
		wantSizes := map[string]int64{"alpine-3.24": 3460064, "alpine-3.23": 3450920, "alpine-3.22": 3271244}
		for _, image := range imageCatalog.Data {
			if size, ok := wantSizes[image.ID]; ok {
				if image.Custom || image.Type != "lxc" || image.Arch != "amd64" || image.SizeBytes != size {
					t.Fatalf("Proxmox archive is not displayed as a built-in LXC image: %+v", image)
				}
				delete(wantSizes, image.ID)
			}
		}
		if len(wantSizes) != 0 {
			t.Fatalf("missing images in the management catalog: %v", wantSizes)
		}
		return
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/images/download", strings.NewReader(`{"template_id":"alpine-3.24"}`))
	req.Header.Set("Authorization", authorization)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "retired from the download catalog") {
		t.Fatalf("retired image download response = %d %s", rec.Code, rec.Body.String())
	}
}

func imageAccessToken(t *testing.T, tenant string) string {
	t.Helper()
	claims := jwt.MapClaims{"username": "admin", "exp": time.Now().Add(time.Minute).Unix()}
	if tenant != "" {
		claims["sub_user"] = tenant
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(config.AppConfig.JWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return token
}
