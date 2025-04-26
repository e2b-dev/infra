package template_errors

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

type ReturnedError[T any] struct {
	template *template.Template
	vars     T
}

func (e *ReturnedError[T]) BuildHtml() ([]byte, error) {
	return buildHtmlError(e.template, e.vars)
}

func (e *ReturnedError[T]) BuildJson() ([]byte, error) {
	return json.Marshal(e.vars)
}
