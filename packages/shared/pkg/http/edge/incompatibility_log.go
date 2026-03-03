package edge

import (
	"context"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func WarnMissingFeatureHeader(ctx context.Context, featureHeader string, msg string, fields ...zap.Field) {
	logFields := make([]zap.Field, 0, len(fields)+2)
	logFields = append(
		logFields,
		zap.String("incompatibility_type", "missing_feature_header"),
		zap.String("feature_header", featureHeader),
	)
	logFields = append(logFields, fields...)

	logger.L().Warn(ctx, "edge incompatible with api contract: "+msg, logFields...)
}
