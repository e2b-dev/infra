package joined

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Mark must be safe even when the context carries no holder.
func TestMark_NoHolder_Noop(t *testing.T) {
	t.Parallel()
	Mark(context.Background())
}

// Attribute must return request.joined=false when no holder is on ctx.
func TestAttribute_NoHolder_ReturnsFalse(t *testing.T) {
	t.Parallel()

	a := Attribute(context.Background())
	assert.Equal(t, AttributeKey, string(a.Key))
	assert.False(t, a.Value.AsBool())
}

// Attribute must return request.joined=false on a freshly installed holder
// before Mark has been called.
func TestAttribute_FreshHolder_ReturnsFalse(t *testing.T) {
	t.Parallel()

	ctx := WithHolder(context.Background())

	a := Attribute(ctx)
	assert.False(t, a.Value.AsBool())
}

// Mark must flip Attribute to true on the same ctx.
func TestMark_FlipsAttributeToTrue(t *testing.T) {
	t.Parallel()

	ctx := WithHolder(context.Background())
	Mark(ctx)

	a := Attribute(ctx)
	assert.True(t, a.Value.AsBool())
}

// WithHolder must be idempotent: calling it twice returns a ctx that shares
// the same underlying holder (Mark on the first ctx is visible from the
// second).
func TestWithHolder_Idempotent(t *testing.T) {
	t.Parallel()

	ctx1 := WithHolder(context.Background())
	ctx2 := WithHolder(ctx1)

	Mark(ctx1)

	a := Attribute(ctx2)
	assert.True(t, a.Value.AsBool(), "second WithHolder must reuse the first holder")
}

// Mark must be safe when called from a goroutine descended from the
// request context.
func TestMark_DescendantGoroutine(t *testing.T) {
	t.Parallel()

	ctx := WithHolder(context.Background())
	done := make(chan struct{})
	go func() {
		Mark(ctx)
		close(done)
	}()
	<-done

	a := Attribute(ctx)
	assert.True(t, a.Value.AsBool())
}
