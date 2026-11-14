package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"clicd/internal/config"
)

func updateWHMCSHostnames(w http.ResponseWriter, r *http.Request, c *config.Container) {
	var req struct {
		DisplayName    *string `json:"display_name"`
		SystemHostname *string `json:"system_hostname"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || req.DisplayName == nil || decoder.Decode(&struct{}{}) != io.EOF || !config.ValidContainerDisplayName(*req.DisplayName) {
		jsonResponse(w, 400, APIResponse{Success: false, Message: "display_name must be a valid hostname or empty"})
		return
	}
	result := map[string]string{"uuid": c.UUID, "name": c.Name, "display_name": *req.DisplayName}
	if req.SystemHostname == nil {
		if _, err := config.SetContainerDisplayName(c.UUID, *req.DisplayName); err != nil {
			jsonResponse(w, 500, APIResponse{Success: false, Message: "Could not persist instance display name"})
			return
		}
	} else {
		if !config.ValidSystemHostname(*req.SystemHostname) {
			jsonResponse(w, 400, APIResponse{Success: false, Message: "system_hostname must be a valid Linux hostname"})
			return
		}
		id, uuid := c.ID, c.UUID
		err := globalQueue.WithIdleContainer(id, func() error {
			current := config.FindContainerByUUID(uuid)
			if current == nil || current.ID != id || current.Runtime() != config.VirtualizationLXC {
				return fmt.Errorf("LXC instance is no longer available")
			}
			if err := lxcManager.SetSystemHostname(current.LxcName(), *req.SystemHostname); err != nil {
				return err
			}
			_, err := config.SetContainerHostnames(uuid, *req.DisplayName, *req.SystemHostname)
			return err
		})
		if errors.Is(err, ErrContainerBusy) {
			jsonResponse(w, 409, APIResponse{Success: false, Message: err.Error()})
			return
		}
		if err != nil {
			fmt.Printf("WHMCS hostname update for LXC %d failed: %v\n", id, err)
			jsonResponse(w, 503, APIResponse{Success: false, Message: "Could not apply and save the system hostname"})
			return
		}
		result["system_hostname"] = *req.SystemHostname
	}
	jsonResponse(w, 200, APIResponse{Success: true, Data: result})
}
