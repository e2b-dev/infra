package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

type resumeBenchOptions struct {
	enabled    bool
	iterations int
	warmup     int
}

var resumeBenchArms = []struct {
	name         string
	useMemfd     bool
	useMemfdWake bool
}{
	{"default", false, false},
	{"memfd-copy", true, false},
	{"memfd-wake", true, true},
}

func (r *runner) resumeBench(ctx context.Context, opts resumeBenchOptions) error {
	if opts.iterations <= 0 {
		return errors.New("resume-bench: iterations must be > 0")
	}
	fmt.Printf("Resume bench (%d iterations per arm, warmup=%d)\n", opts.iterations, opts.warmup)

	results := make(map[string][]time.Duration, len(resumeBenchArms))
	for _, arm := range resumeBenchArms {
		featureflags.OverrideBoolFlag(featureflags.UseMemFdFlag, arm.useMemfd)
		featureflags.OverrideBoolFlag(featureflags.UseMemfdWakeFlag, arm.useMemfdWake)

		for i := range opts.warmup {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if _, err := r.resumeOnce(ctx, i); err != nil {
				return fmt.Errorf("%s warmup: %w", arm.name, err)
			}
		}

		samples := make([]time.Duration, 0, opts.iterations)
		for i := range opts.iterations {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			dur, err := r.resumeOnce(ctx, i)
			if err != nil {
				return fmt.Errorf("%s iter %d: %w", arm.name, i+1, err)
			}
			samples = append(samples, dur)
			fmt.Printf("[%d/%d] %-10s: %s\n", i+1, opts.iterations, arm.name, dur.Round(time.Millisecond))
		}
		results[arm.name] = samples
	}

	fmt.Println()
	for _, arm := range resumeBenchArms {
		s := results[arm.name]
		var sum time.Duration
		for _, d := range s {
			sum += d
		}
		fmt.Printf("%-10s  avg %s  min %s  max %s\n",
			arm.name,
			(sum / time.Duration(len(s))).Round(time.Millisecond),
			slices.Min(s).Round(time.Millisecond),
			slices.Max(s).Round(time.Millisecond),
		)
	}

	return nil
}
