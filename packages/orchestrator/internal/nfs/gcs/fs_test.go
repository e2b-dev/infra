package gcs

import (
	"os"
	"testing"
)

func TestBucketAttrsInverse(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		flag int
		perm os.FileMode
	}{
		{
			name: "basic",
			flag: os.O_RDWR | os.O_CREATE,
			perm: 0o644,
		},
		{
			name: "another",
			flag: os.O_RDONLY,
			perm: 0o755,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			permKey, permVal := fromPermToObjectMetadata(tc.perm)
			metadata := map[string]string{permKey: permVal}
			gotPerm := fromMetadataToPerm(metadata)

			if gotPerm != tc.perm {
				t.Errorf("expected perm %o, got %o", tc.perm, gotPerm)
			}
		})
	}
}

func TestFromBucketAttrs_Empty(t *testing.T) {
	t.Parallel()

	attrs := make(map[string]string)
	gotPerm := fromMetadataToPerm(attrs)

	if gotPerm != 0 {
		t.Errorf("expected perm 0, got %o", gotPerm)
	}
}
