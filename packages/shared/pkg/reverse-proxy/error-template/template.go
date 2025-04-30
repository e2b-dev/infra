package error_template

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"regexp"

	"go.uber.org/zap"
)

func buildHtmlError[T any](template *template.Template, vars T) ([]byte, error) {
	html := new(bytes.Buffer)

	err := template.Execute(html, vars)
	if err != nil {
		return nil, err
	}

	return html.Bytes(), nil
}

type TemplatedError[T any] struct {
	template *template.Template
	vars     T
	status   int
}

func (e *TemplatedError[T]) buildHtml() ([]byte, error) {
	return buildHtmlError(e.template, e.vars)
}

func (e *TemplatedError[T]) buildJson() ([]byte, error) {
	return json.Marshal(e.vars)
}

var browserRegex = regexp.MustCompile(`(?i)mozilla|chrome|safari|firefox|edge|opera|msie`)

func isBrowser(r *http.Request) bool {
	return browserRegex.MatchString(r.UserAgent())
}

func (e *TemplatedError[T]) HandleError(
	w http.ResponseWriter,
	r *http.Request,
	logger *zap.Logger,
) error {
	if isBrowser(r) {
		body, buildErr := e.buildHtml()
		if buildErr != nil {
			return buildErr
		}

		w.WriteHeader(e.status)
		w.Header().Add("Content-Type", "text/html")
		_, writeErr := w.Write(body)
		if writeErr != nil {
			return writeErr
		}

		return nil
	}

	body, buildErr := e.buildJson()
	if buildErr != nil {
		return buildErr
	}

	w.WriteHeader(e.status)
	w.Header().Add("Content-Type", "application/json")

	_, writeErr := w.Write(body)
	if writeErr != nil {
		return writeErr
	}

	return nil
}
