package writer

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"go.uber.org/zap"
)

type BuildLogsWriter struct {
	stream template_manager.TemplateService_TemplateCreateServer
	logger *zap.Logger
}

func (w BuildLogsWriter) Write(p []byte) (n int, err error) {
	log := string(p)
	w.logger.Info(log)
	err = w.stream.Send(&template_manager.TemplateBuildLog{Log: log})
	if err != nil {
		return 0, err
	}

	return len(p), nil
}

func New(stream template_manager.TemplateService_TemplateCreateServer, logger *zap.Logger) BuildLogsWriter {
	writer := BuildLogsWriter{
		stream: stream,
		logger: logger,
	}

	return writer
}
