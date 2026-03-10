package template

import (
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
)

//go:embed browser_port_not_allowed.html
var portNotAllowedHtml string
var portNotAllowedHtmlTemplate = template.Must(template.New("portNotAllowedHtml").Parse(portNotAllowedHtml))

//go:embed browser_client_ip_not_allowed.html
var clientIPNotAllowedHtml string
var clientIPNotAllowedHtmlTemplate = template.Must(template.New("clientIPNotAllowedHtml").Parse(clientIPNotAllowedHtml))

type portNotAllowedData struct {
	SandboxId string `json:"sandboxId"`
	Port      uint64 `json:"port"`
	Message   string `json:"message"`
	Code      int    `json:"code"`
	Host      string `json:"-"`
}

func (e portNotAllowedData) StatusCode() int {
	return e.Code
}

func NewPortNotAllowedError(sandboxId string, host string, port uint64) *TemplatedError[portNotAllowedData] {
	return &TemplatedError[portNotAllowedData]{
		template: portNotAllowedHtmlTemplate,
		vars: portNotAllowedData{
			SandboxId: sandboxId,
			Port:      port,
			Message:   fmt.Sprintf("Access to port %d is not allowed", port),
			Host:      host,
			Code:      http.StatusForbidden,
		},
	}
}

type clientIPNotAllowedData struct {
	SandboxId string `json:"sandboxId"`
	ClientIP  string `json:"clientIp"`
	Message   string `json:"message"`
	Code      int    `json:"code"`
	Host      string `json:"-"`
}

func (e clientIPNotAllowedData) StatusCode() int {
	return e.Code
}

func NewClientIPNotAllowedError(sandboxId string, host string, clientIP string) *TemplatedError[clientIPNotAllowedData] {
	return &TemplatedError[clientIPNotAllowedData]{
		template: clientIPNotAllowedHtmlTemplate,
		vars: clientIPNotAllowedData{
			SandboxId: sandboxId,
			ClientIP:  clientIP,
			Message:   "Your IP address is not allowed to access this sandbox",
			Host:      host,
			Code:      http.StatusForbidden,
		},
	}
}
