package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestNativeDownloadCatalogRejectsRetiredMirrorPreset(t *testing.T) {
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
	var response struct {
		Data []struct{ ID, URL string }
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil ||
		len(response.Data) != 10 || response.Data[0].ID != "ubuntu-noble" || response.Data[5].ID != "alpine-3.21" {
		t.Fatalf("unexpected template catalog: %d %s", rec.Code, rec.Body.String())
	}

	for _, template := range response.Data {
		if template.ID == "alpine-3.24" || template.URL != "" {
			t.Fatalf("unexpected download from the former mirror catalog: %+v", template)
		}
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
