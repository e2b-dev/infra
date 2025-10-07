package limits

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	_ "modernc.org/sqlite"
)

func TestConcurrentAcquire(t *testing.T) {

	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:32768",
	})

	l := New(uuid.NewString(), redisClient)

	var wg sync.WaitGroup
	var timeouts atomic.Int32
	var successes atomic.Int32
	var unknownErrs atomic.Int32

	for range 10 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			err := l.TryAcquire(t.Context(), "test", 5)
			if err == nil {
				successes.Add(1)
			} else if errors.Is(err, ErrTimeout) {
				timeouts.Add(1)
			} else {
				unknownErrs.Add(1)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int32(5), timeouts.Load(), "should have 5 acquisition timeouts")
	assert.Equal(t, int32(5), successes.Load(), "should have 5 successes")
	assert.Equal(t, int32(0), unknownErrs.Load(), "should have 0 unknown errors")
}
