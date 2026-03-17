package template

import (
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
)

//go:embed browser_ingress_denied.html
var ingressDeniedHtml string
var ingressDeniedHtmlTemplate = template.Must(template.New("ingressDeniedHtml").Parse(ingressDeniedHtml))

type ingressDeniedData struct {
	SandboxId string `json:"sandboxId"`
	ClientIP  string `json:"clientIp"`
	Port      uint16 `json:"port"`
	Message   string `json:"message"`
	Code      int    `json:"code"`
	Host      string `json:"-"`
}

func (e ingressDeniedData) StatusCode() int {
	return e.Code
}

func NewIngressDeniedError(sandboxId string, host string, clientIP string, port uint16) *TemplatedError[ingressDeniedData] {
	return &TemplatedError[ingressDeniedData]{
		template: ingressDeniedHtmlTemplate,
		vars: ingressDeniedData{
			SandboxId: sandboxId,
			ClientIP:  clientIP,
			Port:      port,
			Message:   fmt.Sprintf("Access denied: client %s is not allowed to reach port %d on this sandbox", clientIP, port),
			Host:      host,
			Code:      http.StatusForbidden,
		},
	}
}
