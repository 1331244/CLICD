package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"clicd/internal/config"
	"clicd/internal/lxc"
)

var renameLXCImageDownloadedInfo = lxcTemplateDownloadedInfo
var renameLXCImageArchive = lxc.RenameImageArchive

// HandleImageRename changes a cached LXC image's display name and archive filename.
// Image IDs and native cache entry points remain stable.
func HandleImageRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		jsonResponse(w, http.StatusMethodNotAllowed, APIResponse{Success: false, Message: "Method not allowed"})
		return
	}
	if !requireScope(w, r, "image:toggle") {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16*1024))
	if err != nil || !utf8.Valid(body) {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid rename request"})
		return
	}
	var request struct {
		TemplateID string `json:"template_id"`
		Name       string `json:"name"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(interface{})) != io.EOF {
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Provide template_id and name only"})
		return
	}
	request.TemplateID = strings.TrimSpace(request.TemplateID)
	name, err := config.NormalizeImageDisplayName(request.Name)
	if err != nil || request.TemplateID == "" || len(request.TemplateID) > 255 {
		message := "template_id required"
		if err != nil {
			message = err.Error()
		}
		jsonResponse(w, http.StatusBadRequest, APIResponse{Success: false, Message: message})
		return
	}

	// Serialize renames and source registration/removal so uniqueness uses the current catalog.
	imageMetadataMu.Lock()
	defer imageMetadataMu.Unlock()
	templates := lxc.GetTemplates()
	var selected *lxc.Template
	for index := range templates {
		if templates[index].ID == request.TemplateID {
			selected = &templates[index]
			break
		}
	}
	if selected == nil {
		jsonResponse(w, http.StatusNotFound, APIResponse{Success: false, Message: "LXC template not found"})
		return
	}
	if isImageDownloadActive(selected.ID) {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Image is downloading; wait before renaming"})
		return
	}
	if imageHasActiveTask(selected.ID) || savedImageHasActiveTask(selected.ID) {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Image is referenced by an active task; wait before renaming"})
		return
	}
	if downloaded, _ := renameLXCImageDownloadedInfo(*selected); !downloaded {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Download the image before renaming"})
		return
	}
	for _, template := range templates {
		if template.ID != selected.ID && strings.EqualFold(template.Name, name) {
			jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "An image with this name already exists"})
			return
		}
	}
	if err := renameLXCImageArchive(*selected, name); err != nil {
		jsonResponse(w, http.StatusConflict, APIResponse{Success: false, Message: "Cannot rename cached image: " + err.Error()})
		return
	}
	filename := config.ImageCacheFilenames()[selected.ID]
	jsonResponse(w, http.StatusOK, APIResponse{Success: true, Message: "Image name and archive filename updated", Data: map[string]string{"id": selected.ID, "name": name, "filename": filename, "type": config.VirtualizationLXC}})
}

func savedImageHasActiveTask(id string) bool {
	for _, task := range config.AppConfig.Tasks {
		if task.Status != "pending" && task.Status != "running" {
			continue
		}
		var taskConfig struct {
			TemplateID string `json:"template_id"`
		}
		_ = json.Unmarshal([]byte(task.Config), &taskConfig)
		if task.TemplateID == id || taskConfig.TemplateID == id {
			return true
		}
	}
	return false
}
