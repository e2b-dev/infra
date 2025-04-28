package error_template

import (
	"bytes"
	"encoding/json"
	"html/template"
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

func (e *TemplatedError[T]) BuildHtml() ([]byte, error) {
	return buildHtmlError(e.template, e.vars)
}

func (e *TemplatedError[T]) BuildJson() ([]byte, error) {
	return json.Marshal(e.vars)
}

func (e *TemplatedError[T]) Status() int {
	return e.status
}
