package template

import (
	_ "embed"
	"html/template"
	"net/http"
)

//go:embed browser_sandbox_paused.html
var sandboxPausedHtml string
var sandboxPausedHtmlTemplate = template.Must(template.New("sandboxPausedHtml").Parse(sandboxPausedHtml))

type sandboxPausedData struct {
	SandboxId     string `json:"sandboxId"`
	Message       string `json:"message"`
	Code          int    `json:"code"`
	CanAutoResume bool   `json:"canAutoResume"`
	Host          string `json:"-"`
}

func (e sandboxPausedData) StatusCode() int {
	return e.Code
}

func NewSandboxPausedError(sandboxId, host string, canAutoResume bool) *TemplatedError[sandboxPausedData] {
	return &TemplatedError[sandboxPausedData]{
		template: sandboxPausedHtmlTemplate,
		vars: sandboxPausedData{
			SandboxId:     sandboxId,
			Message:       "The sandbox is paused",
			Host:          host,
			CanAutoResume: canAutoResume,
			Code:          http.StatusConflict,
		},
	}
}
