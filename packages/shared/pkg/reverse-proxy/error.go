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
) {
	if isBrowser(r) {
		body, buildErr := err.BuildHtml()
		if buildErr != nil {
			logger.Error("Failed to build HTML error response", zap.Error(buildErr))
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		w.WriteHeader(http.StatusBadGateway)
		w.Header().Add("Content-Type", "text/html")
		_, writeErr := w.Write(body)
		if writeErr != nil {
			logger.Error("failed to write HTML error response", zap.Error(writeErr))
		}

		return
	}

	body, buildErr := err.BuildJson()
	if buildErr != nil {
		logger.Error("failed to build JSON error response", zap.Error(buildErr))

		return
	}

	w.WriteHeader(http.StatusBadGateway)
	w.Header().Add("Content-Type", "application/json")

	_, writeErr := w.Write(body)
	if writeErr != nil {
		logger.Error("failed to write JSON error response", zap.Error(writeErr))
	}
}
