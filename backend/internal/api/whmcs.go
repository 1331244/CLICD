package api

import (
	"bytes"
	"clicd/internal/config"
	"clicd/internal/lxc"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var whmcsLocks sync.Map
var whmcsInstanceEpoch = randomHex(16)

func ValidateWHMCSEpoch(w http.ResponseWriter, r *http.Request) bool {
	if epoch := r.Header.Get("X-CLICD-Process-Epoch"); epoch != "" && epoch != whmcsInstanceEpoch {
		jsonResponse(w, 409, APIResponse{Success: false, Message: "WHMCS_PROCESS_EPOCH_CHANGED"})
		return false
	}
	return true
}
func whmcsLock(uuid string) func() {
	value, _ := whmcsLocks.LoadOrStore(uuid, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
func whmcsFingerprint(v interface{}) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	sum := sha256.Sum256(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:])
}

// ConditionalContainerMiddleware serializes native and billing API mapping edits
// on the same UUID, including edits from the native panel, and supports If-Match.
func ConditionalContainerMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/v1/containers/"), "/api/containers/")
		parts := strings.SplitN(path, "/", 2)
		c := containerByIdentifier(parts[0])
		if c == nil {
			next(w, r)
			return
		}
		unlock := whmcsLock(c.UUID)
		defer unlock()
		c = containerByIdentifier(parts[0])
		if c == nil {
			next(w, r)
			return
		}
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}
		maintenance := (action == "expiry" || action == "resource-limit" || action == "traffic-limit") && !isAccessRestrictedRequest(r) && hasScope(r, "admin:access")
		if c.BillingBlocked() && r.Method != "GET" && action != "stop" && action != "delete" && !maintenance {
			jsonResponse(w, 403, APIResponse{Success: false, Message: "Instance is restricted by a billing owner"})
			return
		}
		if match := r.Header.Get("If-Match"); match != "" && r.Method != "GET" {
			var value interface{}
			if strings.HasPrefix(action, "ipv6-port-mappings") {
				maps := c.IPv6PortMappings
				if maps == nil {
					maps = []config.PortMapping{}
				}
				value = maps
			} else if strings.HasPrefix(action, "port-mappings") {
				maps := c.PortMappings
				if maps == nil {
					maps = []config.PortMapping{}
				}
				value = maps
			} else if action == "firewall" {
				value = map[string]interface{}{"enabled": c.FirewallEnabled, "default_action": normalizeFirewallDefaultAction(c.FirewallDefaultAction), "rules": c.FirewallRules}
			}
			if value == nil || whmcsFingerprint(value) != match {
				jsonResponse(w, 412, APIResponse{Success: false, Message: "Configuration changed; refresh before editing"})
				return
			}
		}
		next(w, r)
	}
}

// HandleWHMCS is an additive, explicitly versioned compatibility extension.
// It is unavailable on stock 1.1.38. Never grant its key to a browser.
func HandleWHMCS(w http.ResponseWriter, r *http.Request) {
	if !requireTransferAdmin(w, r) {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/whmcs/")
	if path == "capabilities" && r.Method == "GET" {
		start, end := config.NATPortRange()
		portRange := map[string]int{"start": start, "end": end}
		jsonResponse(w, 200, APIResponse{Success: true, Data: map[string]interface{}{"version": "1", "virtualization": "lxc", "billing_reasons": true, "cpu_percent": true, "swap_mb": true, "ssh_key_offline": true, "conditional_mappings": true, "uuid_tickets": true, "paid_traffic_reset": true, "traffic_quota_2mbps": lxc.WHMCSTrafficQuotaAvailable(), "image_rename": true, "manual_port_mappings": true, "fixed_port_allocations": true, "instance_display_name": true, "nat4_port_range": portRange, "nat6_port_range": portRange, "instance_epoch": whmcsInstanceEpoch}})
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) != 3 || parts[0] != "containers" || (r.Method != "PUT" && !(parts[2] == "traffic-reset" && r.Method == "GET")) {
		http.NotFound(w, r)
		return
	}
	c := config.FindContainerByUUID(parts[1])
	if c == nil {
		jsonResponse(w, 404, APIResponse{Success: false, Message: "LXC instance not found"})
		return
	}
	if c.Runtime() != "lxc" {
		jsonResponse(w, 409, APIResponse{Success: false, Message: "LXC instance required"})
		return
	}
	unlock := whmcsLock(c.UUID)
	defer unlock()
	if parts[2] == "display-name" {
		if !ValidateWHMCSEpoch(w, r) {
			return
		}
		var req struct {
			DisplayName *string `json:"display_name"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		d.DisallowUnknownFields()
		if d.Decode(&req) != nil || req.DisplayName == nil || d.Decode(&struct{}{}) != io.EOF || !config.ValidContainerDisplayName(*req.DisplayName) {
			jsonResponse(w, 400, APIResponse{Success: false, Message: "display_name must be a valid hostname or empty"})
			return
		}
		if _, err := config.SetContainerDisplayName(c.UUID, *req.DisplayName); err != nil {
			jsonResponse(w, 500, APIResponse{Success: false, Message: "Could not persist instance display name"})
			return
		}
		jsonResponse(w, 200, APIResponse{Success: true, Data: map[string]string{"uuid": c.UUID, "name": c.Name, "display_name": *req.DisplayName}})
		return
	}
	if parts[2] == "traffic-reset" {
		if r.Method == "GET" {
			data, err := lxcManager.WHMCSTrafficState(c.ID, r.URL.Query().Get("request_key"))
			if err != nil {
				jsonResponse(w, 503, APIResponse{Success: false, Message: err.Error()})
				return
			}
			jsonResponse(w, 200, APIResponse{Success: true, Data: data})
			return
		}
		var req struct {
			Key   string `json:"request_key"`
			Cycle string `json:"cycle"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		d.DisallowUnknownFields()
		if d.Decode(&req) != nil || !regexp.MustCompile(`^whmcs-[a-f0-9]{16}-[1-9][0-9]*:[a-f0-9]{32}$`).MatchString(req.Key) || !regexp.MustCompile(`^[0-9]{4}-[0-9]{2}$`).MatchString(req.Cycle) {
			jsonResponse(w, 400, APIResponse{Success: false, Message: "Invalid traffic reset intent"})
			return
		}
		receipt, err := lxcManager.WHMCSResetPaidTraffic(c.ID, req.Key, req.Cycle)
		if err != nil {
			jsonResponse(w, 409, APIResponse{Success: false, Message: err.Error()})
			return
		}
		jsonResponse(w, 200, APIResponse{Success: true, Data: receipt})
		return
	}
	if parts[2] == "ssh-key" {
		var request struct {
			PublicKey string `json:"public_key"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		d.DisallowUnknownFields()
		if d.Decode(&request) != nil || c.BillingBlocked() {
			jsonResponse(w, 400, APIResponse{Success: false, Message: "Invalid key request or billing restriction"})
			return
		}
		if err := lxcManager.SetWHMCSPublicKey(c.ID, request.PublicKey); err != nil {
			jsonResponse(w, 409, APIResponse{Success: false, Message: err.Error()})
			return
		}
		config.AddAuditLog("whmcs.ssh-key", c.Name, "Public key added", requestActor(r))
		jsonResponse(w, 200, APIResponse{Success: true})
		return
	}
	if parts[2] != "policy" {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Owner               string `json:"owner"`
		Blocked             *bool  `json:"blocked"`
		CPUPercent          *int   `json:"cpu_percent"`
		SwapMB              *int   `json:"swap_mb"`
		PortLimit           *int   `json:"port_mapping_limit"`
		IPv6PortLimit       *int   `json:"ipv6_port_mapping_limit"`
		FixedPortAllocation *bool  `json:"fixed_port_allocation"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		jsonResponse(w, 400, APIResponse{Success: false, Message: "Invalid policy"})
		return
	}
	old := *c
	next := old
	fail := func(msg string) { jsonResponse(w, 400, APIResponse{Success: false, Message: msg}) }
	if req.Blocked != nil {
		if !regexp.MustCompile(`^whmcs-[a-f0-9]{16}-[1-9][0-9]*$`).MatchString(req.Owner) {
			fail("A stable billing owner is required")
			return
		}
		next.BillingRestrictions = map[string]bool{}
		for key, value := range c.BillingRestrictions {
			next.BillingRestrictions[key] = value
		}
		if *req.Blocked {
			next.BillingRestrictions[req.Owner] = true
		} else {
			delete(next.BillingRestrictions, req.Owner)
		}
	}
	if req.CPUPercent != nil {
		if *req.CPUPercent < 0 || *req.CPUPercent > 100 {
			fail("cpu_percent must be 0..100")
			return
		}
		next.CPUPercent = *req.CPUPercent
	}
	if req.SwapMB != nil {
		if *req.SwapMB < 0 || *req.SwapMB > 1048576 {
			fail("swap_mb is outside bounds")
			return
		}
		next.SwapMB = *req.SwapMB
		next.SwapConfigured = true
	}
	if req.PortLimit != nil {
		if *req.PortLimit < 0 || *req.PortLimit > 64 || *req.PortLimit < len(c.PortMappings) {
			fail("NAT quota is below current use or outside bounds")
			return
		}
		next.PortMappingLimit = *req.PortLimit
	}
	if req.IPv6PortLimit != nil {
		if *req.IPv6PortLimit < 0 || *req.IPv6PortLimit > 64 || (*req.IPv6PortLimit > 0 && *req.IPv6PortLimit < len(c.IPv6PortMappings)) {
			fail("IPv6 NAT quota is below current use or outside bounds")
			return
		}
		next.IPv6PortMappingLimit = *req.IPv6PortLimit
	}
	if req.FixedPortAllocation != nil {
		next.FixedPortAllocation = *req.FixedPortAllocation
	}
	var applyPolicy func(*config.Container) error
	if !next.BillingBlocked() && (req.CPUPercent != nil || req.SwapMB != nil) {
		applyPolicy = lxcManager.ApplyContainerLimits
	}
	if err := lxc.ChangeFixedPortPolicy(c, next, applyPolicy); err != nil {
		status := 400
		if errors.Is(err, lxc.ErrFixedPortPersistence) || errors.Is(err, lxc.ErrFixedPortApply) {
			status = 500
		}
		jsonResponse(w, status, APIResponse{Success: false, Message: err.Error()})
		return
	}
	if c.BillingBlocked() {
		webSSHTickets.Lock()
		for ticket, item := range webSSHTickets.items {
			if item.ContainerName == c.Name {
				delete(webSSHTickets.items, ticket)
			}
		}
		webSSHTickets.Unlock()
		if c.Status == "running" {
			if err := lxcManager.StopContainer(c.ID); err != nil {
				jsonResponse(w, 500, APIResponse{Success: false, Message: fmt.Sprintf("Restriction persisted; stop failed: %v", err)})
				return
			}
		}
	}
	config.AddAuditLog("whmcs.policy", c.Name, "Billing policy updated", requestActor(r))
	jsonResponse(w, 200, APIResponse{Success: true, Data: map[string]interface{}{"blocked": c.BillingBlocked(), "cpu_percent": c.CPUPercent, "swap_mb": c.SwapMB, "fixed_port_allocation": c.FixedPortAllocation, "nat_port_allocations": c.NATPortAllocations, "ipv6_nat_port_allocations": c.IPv6NATPortAllocations}})
}

// Existing terminal sockets are closed on restrictions, expiry, or deletion.
func watchWHMCSRestriction(uuid string, closeSession func(), done <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			c := config.FindContainerByUUID(uuid)
			if c == nil || c.BillingBlocked() || lxc.IsExpired(*c) {
				closeSession()
				return
			}
		}
	}
}
