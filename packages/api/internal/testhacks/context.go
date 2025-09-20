package testhacks

import "context"

type testNameContextKey struct{}

func addTestName(ctx context.Context, testName string) context.Context {
	return context.WithValue(ctx, testNameContextKey{}, testName)
}

func GetTestName(ctx context.Context) string {
	val, _ := ctx.Value(testNameContextKey{}).(string)
	return val
}
