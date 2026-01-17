package gcs

import (
	"os"
	"testing"

	"cloud.google.com/go/storage"
)

func TestBucketAttrsInverse(t *testing.T) {
	testCases := []struct {
		name string
		flag int
		perm os.FileMode
	}{
		{
			name: "basic",
			flag: os.O_RDWR | os.O_CREATE,
			perm: 0644,
		},
		{
			name: "another",
			flag: os.O_RDONLY,
			perm: 0755,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			attrs := toObjectMetadata(tc.flag, tc.perm)
			gotFlag, gotPerm := fromBucketAttrs(attrs)

			if gotFlag != tc.flag {
				t.Errorf("expected flag %o, got %o", tc.flag, gotFlag)
			}
			if gotPerm != tc.perm {
				t.Errorf("expected perm %o, got %o", tc.perm, gotPerm)
			}
		})
	}
}

func TestFromBucketAttrs_Empty(t *testing.T) {
	attrs := &storage.ObjectAttrs{}
	gotFlag, gotPerm := fromBucketAttrs(attrs)

	if gotFlag != 0 {
		t.Errorf("expected flag 0, got %o", gotFlag)
	}
	if gotPerm != 0 {
		t.Errorf("expected perm 0, got %o", gotPerm)
	}
}
