package error_template

import (
	_ "embed"
	"html/template"
)

//go:embed browser_sandbox_not_found.html
var sandboxNotFoundHtml string
var sandboxNotFoundHtmlTemplate = template.Must(template.New("sandboxNotFoundHtml").Parse(sandboxNotFoundHtml))

type sandboxNotFoundData struct {
	SandboxId string `json:"sandboxId"`
	Message   string `json:"message"`
	Host      string `json:"-"`
}

func NewSandboxNotFoundError(sandboxId string, host string) *TemplatedError[sandboxNotFoundData] {
	return &TemplatedError[sandboxNotFoundData]{
		template: sandboxNotFoundHtmlTemplate,
		vars: sandboxNotFoundData{
			SandboxId: sandboxId,
			Message:   "The sandbox was not found",
			Host:      host,
		},
	}
}
