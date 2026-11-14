package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"clicd/internal/config"
)

func TestWHMCSInstanceDisplayNameContract(t *testing.T) {
	const fixtureEnv = "CLICD_INSTANCE_DISPLAY_NAME_TEST_DIR"
	dir := os.Getenv(fixtureEnv)
	if dir == "" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(executable, "-test.run=^TestWHMCSInstanceDisplayNameContract$", "-test.v")
		cmd.Env = append(os.Environ(), fixtureEnv+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated display-name fixture: %v\n%s", err, output)
		} else {
			t.Log(string(output))
		}
		return
	}
	fixture := config.ClicdConfig{AdminUser: "display-fixture", AdminPassHash: "fixture-hash", JWTSecret: "fixture-secret", Port: 8999, DataDir: dir,
		Containers: []config.Container{{ID: 77, UUID: "display-bound-uuid", Name: "whmcs-77-fixed-intent", LXCName: "ct-77", Virtualization: "lxc", Status: "running", PortMappings: []config.PortMapping{{HostPort: 22077, ContainerPort: 80, Protocol: "tcp"}}}, {ID: 78, UUID: "other-runtime-uuid", Name: "other", Virtualization: "kvm", Status: "stopped"}},
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
	request := func(method, identifier string, body []byte, auth *AuthContext, epoch string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/whmcs/containers/"+identifier+"/display-name", bytes.NewReader(body))
		if auth != nil {
			r = withAuthContext(r, *auth)
		}
		if epoch != "" {
			r.Header.Set("X-CLICD-Process-Epoch", epoch)
		}
		rec := httptest.NewRecorder()
		HandleWHMCS(rec, r)
		return rec
	}
	admin := &AuthContext{Type: authTypeAdmin}
	put := func(name string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"display_name": name})
		return request(http.MethodPut, "display-bound-uuid", body, admin, "")
	}
	expect := func(rec *httptest.ResponseRecorder, status int) {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("status=%d want=%d body=%s", rec.Code, status, rec.Body.String())
		}
	}
	before := *config.FindContainerByUUID("display-bound-uuid")
	rec := put("vm77.example.invalid")
	expect(rec, http.StatusOK)
	var response struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data["display_name"] != "vm77.example.invalid" || response.Data["name"] != before.Name || response.Data["uuid"] != before.UUID {
		t.Fatalf("display update changed identity or returned wrong value: %s", rec.Body.String())
	}
	current := *config.FindContainerByUUID(before.UUID)
	// Compare all original fields after stripping only the additive display field.
	originalJSON, _ := json.Marshal(before)
	currentJSON, _ := json.Marshal(current)
	var originalFields, currentFields map[string]interface{}
	_ = json.Unmarshal(originalJSON, &originalFields)
	_ = json.Unmarshal(currentJSON, &currentFields)
	delete(originalFields, "display_name")
	delete(currentFields, "display_name")
	if !reflect.DeepEqual(originalFields, currentFields) {
		t.Fatal("display update changed unrelated container fields")
	}
	for _, auth := range []*AuthContext{nil, {Type: authTypeSubUser}, {Type: authTypeAPIKey, Scopes: []string{"container:write"}}, {Type: authTypeAPIKey, Scopes: []string{"*"}, ContainerUUIDs: []string{before.UUID}}} {
		want := 403
		if auth == nil {
			want = 401
		}
		expect(request(http.MethodPut, before.UUID, []byte(`{"display_name":"forged"}`), auth, ""), want)
	}
	expect(request(http.MethodPut, before.UUID, []byte(`{"display_name":"forged"}`), admin, strings.Repeat("0", 32)), 409)
	expect(request(http.MethodPut, "77", []byte(`{"display_name":"forged"}`), admin, ""), 404)
	expect(request(http.MethodPut, "missing-uuid", []byte(`{"display_name":"forged"}`), admin, ""), 404)
	expect(request(http.MethodPut, "other-runtime-uuid", []byte(`{"display_name":"forged"}`), admin, ""), 409)
	expect(request(http.MethodPut, before.UUID, []byte(`{"display_name":"ignored","system_hostname":"bad_name"}`), admin, ""), 400)
	expect(request(http.MethodPut, before.UUID, []byte(`{"display_name":"newName","system_hostname":"newName"}`), admin, ""), 503)
	if c := config.FindContainerByUUID(before.UUID); c.DisplayName != "vm77.example.invalid" || c.SystemHostname != "" {
		t.Fatal("unavailable guest was reported as synchronized")
	}
	expect(request(http.MethodGet, before.UUID, nil, admin, ""), 404)
	for _, body := range []string{`{`, `{}`, `{"display_name":null}`, `{"display_name":1}`, `{"display_name":"valid","name":"forged"}`, `{"display_name":"valid"}{}`, `{"display_name":"` + strings.Repeat("a", 3000) + `"}`} {
		expect(request(http.MethodPut, before.UUID, []byte(body), admin, ""), 400)
	}
	for _, name := range []string{" with-space", "with-space ", "two words", "https://node.invalid", "node.invalid:8999", "a_b", "-bad", "bad-", "a..b", "a.", "中文", "a\nb", strings.Repeat("a", 64), strings.Repeat("a.", 127) + "a"} {
		expect(put(name), 400)
	}
	for _, name := range []string{"node77", "VM77.example.invalid", strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61), ""} {
		expect(put(name), 200)
	}
	key := &AuthContext{Type: authTypeAPIKey, Scopes: []string{"admin:access"}}
	expect(request(http.MethodPut, before.UUID, []byte(`{"display_name":"key-update.example.invalid"}`), key, whmcsInstanceEpoch), 200)
	database, err := sql.Open("sqlite", strings.TrimSuffix(path, ".json")+".db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TRIGGER reject_display_name_api_save BEFORE INSERT ON containers BEGIN SELECT RAISE(ABORT,'forced display-name API failure'); END`); err != nil {
		t.Fatal(err)
	}
	expect(put("key-update.example.invalid"), 200)
	expect(put("must-rollback.example.invalid"), 500)
	var persisted string
	if err := database.QueryRow(`SELECT display_name FROM containers WHERE uuid=?`, before.UUID).Scan(&persisted); err != nil || persisted != "key-update.example.invalid" {
		t.Fatalf("failed API save escaped SQLite rollback: %q %v", persisted, err)
	}
	if config.FindContainerByUUID(before.UUID).DisplayName != persisted {
		t.Fatal("failed API save changed the in-memory display name")
	}
	if _, err := database.Exec(`DROP TRIGGER reject_display_name_api_save`); err != nil {
		t.Fatal(err)
	}
	capabilities := httptest.NewRecorder()
	HandleWHMCS(capabilities, withAuthContext(httptest.NewRequest(http.MethodGet, "/api/v1/whmcs/capabilities", nil), *admin))
	if !strings.Contains(capabilities.Body.String(), `"instance_display_name":true`) {
		t.Fatal("display-name capability missing")
	}
	if !strings.Contains(capabilities.Body.String(), `"instance_system_hostname":true`) {
		t.Fatal("system hostname capability missing")
	}
	list := httptest.NewRecorder()
	listContainers(list, withAuthContext(httptest.NewRequest(http.MethodGet, "/api/v1/containers", nil), *admin))
	if !strings.Contains(list.Body.String(), `"display_name":"key-update.example.invalid"`) {
		t.Fatalf("list omits display name: %s", list.Body.String())
	}
}
