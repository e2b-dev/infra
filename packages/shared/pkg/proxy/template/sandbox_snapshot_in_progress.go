package template

import (
	_ "embed"
	"html/template"
	"net/http"
)

//go:embed browser_sandbox_snapshot_in_progress.html
var sandboxSnapshotInProgressHtml string

var sandboxSnapshotInProgressHtmlTemplate = template.Must(template.New("sandboxSnapshotInProgressHtml").Parse(sandboxSnapshotInProgressHtml))

type sandboxSnapshotInProgressData struct {
	SandboxId string `json:"sandboxId"`
	Message   string `json:"message"`
	Code      int    `json:"code"`
	Host      string `json:"-"`
}

func (e sandboxSnapshotInProgressData) StatusCode() int {
	return e.Code
}

func NewSandboxSnapshotInProgressError(sandboxId, host, message string) *TemplatedError[sandboxSnapshotInProgressData] {
	if message == "" {
		message = "Sandbox snapshot is currently being created"
	}

	return &TemplatedError[sandboxSnapshotInProgressData]{
		template: sandboxSnapshotInProgressHtmlTemplate,
		vars: sandboxSnapshotInProgressData{
			SandboxId: sandboxId,
			Message:   message,
			Host:      host,
			Code:      http.StatusConflict,
		},
	}
}
