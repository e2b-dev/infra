package reverse_proxy

import (
	"net/http"
	"regexp"

	"go.uber.org/zap"

	template "github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/error-template"
)

var browserRegex = regexp.MustCompile(`(?i)mozilla|chrome|safari|firefox|edge|opera|msie`)

func isBrowser(r *http.Request) bool {
	return browserRegex.MatchString(r.UserAgent())
}

func handleError[T any](
	w http.ResponseWriter,
	r *http.Request,
	err *template.TemplatedError[T],
	logger *zap.Logger,
) error {
	if isBrowser(r) {
		body, buildErr := err.BuildHtml()
		if buildErr != nil {
			return buildErr
		}

		w.WriteHeader(err.Status())
		w.Header().Add("Content-Type", "text/html")
		_, writeErr := w.Write(body)
		if writeErr != nil {
			return writeErr
		}

		return nil
	}

	body, buildErr := err.BuildJson()
	if buildErr != nil {
		return buildErr
	}

	w.WriteHeader(err.Status())
	w.Header().Add("Content-Type", "application/json")

	_, writeErr := w.Write(body)
	if writeErr != nil {
		return writeErr
	}

	return nil
}
