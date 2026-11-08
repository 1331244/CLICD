package api

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"clicd/internal/config"
	"clicd/internal/lxc"
)

func TestPendingManualPortCreateSurvivesProcessRestart(t *testing.T) {
	const dirEnv, phaseEnv = "CLICD_MANUAL_PORT_RESTART_DIR", "CLICD_MANUAL_PORT_RESTART_PHASE"
	dir, phase := os.Getenv(dirEnv), os.Getenv(phaseEnv)
	if dir == "" {
		dir = t.TempDir()
		fixture := config.ClicdConfig{AdminUser: "fixture", AdminPassHash: "hash", JWTSecret: "fixture", DataDir: dir,
			NATPortStart: 30000, NATPortEnd: 30010, NextSSHPort: 30000}
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
			cmd := exec.Command(executable, "-test.run=^TestPendingManualPortCreateSurvivesProcessRestart$", "-test.v")
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
	q := newTaskQueue(1)
	if phase == "write" {
		manual := false
		q.Enqueue(0, "manual-restart", TaskCreate, "alpine-3.21", &lxc.ContainerConfig{
			Name: "manual-restart", TemplateID: "alpine-3.21", PortMappingCount: 4, AutoPortMappings: &manual,
		})
		if err := q.persistTasks(); err != nil {
			t.Fatal(err)
		}
		return
	}
	q.restoreTaskState()
	if len(q.createQueue) != 1 {
		t.Fatalf("pending manual create was not restored: %+v", q.GetTasks())
	}
	cfg := q.createQueue[0].Config
	if cfg.AutoPortMappings == nil || *cfg.AutoPortMappings || cfg.PortMappingCount != 4 {
		t.Fatalf("restored task changed allocation mode: %+v", cfg)
	}
	if err := cfg.NormalizeCreateNATMappings(); err != nil {
		t.Fatal(err)
	}
	port, release, err := lxc.ReserveCreateNATPorts(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if port != 0 || config.AppConfig.NextSSHPort != 30000 {
		t.Fatalf("resumed manual create allocated a port: %d, next %d", port, config.AppConfig.NextSSHPort)
	}
}
