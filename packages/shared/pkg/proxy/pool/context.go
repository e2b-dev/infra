package pool

import "context"

type destinationContextKey struct{}

func WithDestination(ctx context.Context, d *Destination) context.Context {
	return context.WithValue(ctx, destinationContextKey{}, d)
}

func getDestination(ctx context.Context) (*Destination, bool) {
	v := ctx.Value(destinationContextKey{})
	if v == nil {
		return nil, false
	}

	d, ok := v.(*Destination)
	if !ok {
		return nil, false
	}

	return d, true
}
