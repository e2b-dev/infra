package feature_flags

import (
	"context"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
)

type ctxKey struct{}

func CreateContext(ctx context.Context, contexts ...ldcontext.Context) context.Context {
	var val ldcontext.Context

	switch len(contexts) {
	case 0:
		return ctx
	case 1:
		val = contexts[0]
	default:
		val = ldcontext.NewMulti(contexts...)
	}

	ctx = context.WithValue(ctx, ctxKey{}, val)
	return ctx
}

func getContext(ctx context.Context) (ldcontext.Context, bool) {
	if val, ok := ctx.Value(ctxKey{}).(ldcontext.Context); ok {
		return val, ok
	}
	return ldcontext.Context{}, false
}
