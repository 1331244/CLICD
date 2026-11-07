package api

import (
	"clicd/internal/config"
	"clicd/internal/lxc"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWHMCSCapabilitiesRequireUnrestrictedAdmin(t *testing.T) {
	for _, auth := range []AuthContext{{Type: authTypeSubUser}, {Type: authTypeAPIKey, Scopes: []string{"container:read"}}, {Type: authTypeAPIKey, Scopes: []string{"*"}, ContainerUUIDs: []string{"owned"}}} {
		w := httptest.NewRecorder()
		r := withAuthContext(httptest.NewRequest("GET", "/api/v1/whmcs/capabilities", nil), auth)
		HandleWHMCS(w, r)
		if w.Code != 403 {
			t.Fatalf("unsafe capability access %+v: %d", auth, w.Code)
		}
	}
	w := httptest.NewRecorder()
	HandleWHMCS(w, withAuthContext(httptest.NewRequest("GET", "/api/v1/whmcs/capabilities", nil), AuthContext{Type: authTypeAdmin}))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestWHMCSBillingReasonsAndRuntimeGate(t *testing.T) {
	previous := config.AppConfig
	defer func() { config.AppConfig = previous }()
	c := config.Container{ID: 1, UUID: "whmcs-owned", Name: "owned", Virtualization: "lxc", BillingRestrictions: map[string]bool{"other-owner": true, "whmcs-1234567890abcdef-1": false}}
	config.AppConfig = &config.ClicdConfig{Containers: []config.Container{c}}
	if !c.BillingBlocked() {
		t.Fatal("another owner's restriction was ignored")
	}
	if err := lxc.NewManager().StartContainer(1); err == nil || !strings.Contains(err.Error(), "restricted") {
		t.Fatalf("runtime start bypass: %v", err)
	}
	handler := ConditionalContainerMiddleware(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	for _, action := range []string{"start", "restart", "reinstall", "snapshots/foreign/restore", "firewall"} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("POST", "/api/v1/containers/whmcs-owned/"+action, nil))
		if w.Code != 403 {
			t.Fatalf("restriction bypass %s: %d", action, w.Code)
		}
	}
}
func TestWHMCSConditionalMappingEdits(t *testing.T) {
	previous := config.AppConfig
	defer func() { config.AppConfig = previous }()
	config.AppConfig = &config.ClicdConfig{Containers: []config.Container{{ID: 1, UUID: "whmcs-map", Name: "maps", Virtualization: "lxc", PortMappings: []config.PortMapping{{ContainerPort: 80, HostPort: 22001, Protocol: "tcp", Description: "a / b & 中文"}}}}}
	handler := ConditionalContainerMiddleware(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	r := httptest.NewRequest("PUT", "/api/v1/containers/whmcs-map/port-mappings/0", nil)
	r.Header.Set("If-Match", strings.Repeat("0", 64))
	w := httptest.NewRecorder()
	handler(w, r)
	if w.Code != 412 {
		t.Fatal("stale array mutation accepted")
	}
	r = httptest.NewRequest("PUT", "/api/v1/containers/whmcs-map/port-mappings/0", nil)
	r.Header.Set("If-Match", whmcsFingerprint(config.AppConfig.Containers[0].PortMappings))
	w = httptest.NewRecorder()
	handler(w, r)
	if w.Code != 204 {
		t.Fatalf("valid mapping conditional denied: %d", w.Code)
	}
}
func TestWHMCSWebSSHTicketCannotFollowReusedName(t *testing.T) {
	previous := config.AppConfig
	defer func() { config.AppConfig = previous }()
	config.AppConfig = &config.ClicdConfig{Containers: []config.Container{{ID: 1, UUID: "new-uuid", Name: "reused-name", Virtualization: "lxc"}}}
	webSSHTickets.Lock()
	webSSHTickets.items["old-ticket"] = webSSHTicket{ContainerName: "reused-name", ContainerUUID: "old-uuid", ExpiresAt: time.Now().Add(time.Minute)}
	webSSHTickets.Unlock()
	if _, ok := consumeWebSSHTicket("old-ticket", "reused-name"); ok {
		t.Fatal("a ticket followed a reused container name")
	}
}
