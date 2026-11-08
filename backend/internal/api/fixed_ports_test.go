package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"clicd/internal/config"
	"clicd/internal/lxc"
)

func TestFixedPortPolicyAndQueuedPlanSurviveProcessRestart(t *testing.T) {
	const dirEnv, phaseEnv = "CLICD_FIXED_PORT_TEST_DIR", "CLICD_FIXED_PORT_TEST_PHASE"
	dir, phase := os.Getenv(dirEnv), os.Getenv(phaseEnv)
	if dir == "" {
		dir = t.TempDir()
		fixture := config.ClicdConfig{AdminUser: "fixture", AdminPassHash: "hash", JWTSecret: "fixture", DataDir: dir, NATPortStart: 30000, NATPortEnd: 30010, NextSSHPort: 30000,
			Containers: []config.Container{{ID: 1, UUID: "fixed-policy", Name: "fixed-policy", Virtualization: "lxc", PortMappingLimit: 2, PortMappings: []config.PortMapping{{HostPort: 30004, ContainerPort: 22, Protocol: "tcp"}}}}}
		raw, err := json.Marshal(fixture)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"write", "read"} {
			cmd := exec.Command(executable, "-test.run=^TestFixedPortPolicyAndQueuedPlanSurviveProcessRestart$", "-test.v")
			cmd.Env = append(os.Environ(), dirEnv+"="+dir, phaseEnv+"="+phase)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s process: %v\n%s", phase, err, output)
			}
		}
		return
	}
	config.SetConfigPath(filepath.Join(dir, "config.json"))
	if _, err := config.InitConfig(); err != nil {
		t.Fatal(err)
	}
	policy := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := withAuthContext(httptest.NewRequest("PUT", "/api/v1/whmcs/containers/fixed-policy/policy", strings.NewReader(body)), AuthContext{Type: authTypeAdmin})
		HandleWHMCS(w, r)
		return w
	}
	if phase == "write" {
		w := policy(`{"fixed_port_allocation":true,"port_mapping_limit":2}`)
		if w.Code != 200 {
			t.Fatalf("policy: %d %s", w.Code, w.Body.String())
		}
		var response struct {
			Data struct {
				Fixed bool  `json:"fixed_port_allocation"`
				Ports []int `json:"nat_port_allocations"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if !response.Data.Fixed || !reflect.DeepEqual(response.Data.Ports, []int{30004, 30000}) {
			t.Fatalf("policy lost legacy port: %s", w.Body.String())
		}
		w = policy(`{"fixed_port_allocation":true,"port_mapping_limit":2}`)
		if w.Code != 200 || !reflect.DeepEqual(config.AppConfig.Containers[0].NATPortAllocations, response.Data.Ports) {
			t.Fatal("idempotent policy reassigned ports")
		}
		if w := policy(`{"fixed_port_allocation":false}`); w.Code != 400 {
			t.Fatal("fixed mode could be silently disabled")
		}
		manual := false
		plans, err := lxc.ReserveBatchCreateNATPorts([]lxc.ContainerConfig{{Name: "fixed-queued", FixedPortAllocation: true, AutoPortMappings: &manual, PortMappingCount: 2}})
		if err != nil {
			t.Fatal(err)
		}
		q := newTaskQueue(1)
		q.Enqueue(0, plans[0].Name, TaskCreate, "alpine-3.21", &plans[0])
		if err := q.persistTasks(); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !config.AppConfig.Containers[0].FixedPortAllocation || !reflect.DeepEqual(config.AppConfig.Containers[0].NATPortAllocations, []int{30004, 30000}) {
		t.Fatal("container fixed allocation lost across processes")
	}
	if port, err := config.PreviewSSHPortExcluding(nil); err != nil || port != 30003 {
		t.Fatalf("queued pools exposed before task recovery: %d %v", port, err)
	}
	q := newTaskQueue(1)
	q.restoreTaskState()
	if len(q.createQueue) != 1 {
		t.Fatal("fixed create task not restored")
	}
	cfg := q.createQueue[0].Config
	if !cfg.FixedPortAllocation || !reflect.DeepEqual(cfg.NATPortAllocations, []int{30001, 30002}) {
		t.Fatalf("modern task payload lost fixed plan: %+v", cfg)
	}
	port, release, err := lxc.ReserveCreateNATPorts(cfg)
	if err != nil || port != 0 {
		t.Fatalf("restored fixed plan cannot claim its ports: %d %v", port, err)
	}
	release()
}
