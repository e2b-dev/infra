package template

import (
	_ "embed"
	"html/template"
	"net/http"
)

//go:embed browser_port_closed.html
var portClosedHtml string
var portClosedHtmlTemplate = template.Must(template.New("portClosedHtml").Parse(portClosedHtml))

type portClosedError struct {
	SandboxId string `json:"sandboxId"`
	Message   string `json:"message"`
	Port      string `json:"port"`
	Host      string `json:"-"`
}

func NewPortClosedError(sandboxId, host, port string) *TemplatedError[portClosedError] {
	return &TemplatedError[portClosedError]{
		template: portClosedHtmlTemplate,
		vars: portClosedError{
			Message:   "The sandbox is running but port is not open",
			SandboxId: sandboxId,
			Host:      host,
			Port:      port,
		},
		status: http.StatusBadGateway,
	}
}
