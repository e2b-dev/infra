package template

import (
	_ "embed"
	"fmt"
	"html/template"
	"net/http"
)

//go:embed browser_traffic_access_token_missing_error.html
var missingTrafficAccessTokenErrHtml string
var missingTrafficAccessTokenErrHtmlTemplate = template.Must(template.New("missingTrafficAccessTokenErrHtml").Parse(missingTrafficAccessTokenErrHtml))

//go:embed browser_traffic_access_token_invalid_error.html
var invalidTrafficAccessTokenErrHtml string
var invalidTrafficAccessTokenErrHtmlTemplate = template.Must(template.New("invalidTrafficAccessTokenErrHtml").Parse(invalidTrafficAccessTokenErrHtml))

type trafficAccessTokenErrData struct {
	SandboxId string `json:"sandboxId"`
	Message   string `json:"message"`
	Code      int    `json:"code"`
	Host      string `json:"-"`
}

func (e trafficAccessTokenErrData) StatusCode() int {
	return e.Code
}

func NewTrafficAccessTokenMissingHeader(sandboxId, host string, header string) *TemplatedError[trafficAccessTokenErrData] {
	return &TemplatedError[trafficAccessTokenErrData]{
		template: missingTrafficAccessTokenErrHtmlTemplate,
		vars: trafficAccessTokenErrData{
			SandboxId: sandboxId,
			Message:   fmt.Sprintf("Sandbox is secured with traffic access token. Token header '%s' is missing", header),
			Host:      host,
			Code:      http.StatusForbidden,
		},
	}
}

func NewTrafficAccessTokenInvalidHeader(sandboxId, host string) *TemplatedError[trafficAccessTokenErrData] {
	return &TemplatedError[trafficAccessTokenErrData]{
		template: invalidTrafficAccessTokenErrHtmlTemplate,
		vars: trafficAccessTokenErrData{
			SandboxId: sandboxId,
			Message:   "Sandbox is secured with traffic access token. Provided token is invalid.",
			Host:      host,
			Code:      http.StatusForbidden,
		},
	}
}
