package dockerhub

import (
	"testing"
)

func TestRemoveRegistryFromTag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		tag     string
		want    string
		wantErr bool
	}{
		{
			name:    "tag with registry prefix",
			tag:     "docker.io/library/ubuntu:latest",
			want:    "library/ubuntu:latest",
			wantErr: false,
		},
		{
			name:    "tag without registry",
			tag:     "ubuntu:latest",
			want:    "library/ubuntu:latest",
			wantErr: false,
		},
		{
			name:    "tag without registry",
			tag:     "index.docker.io/ubuntu:latest",
			want:    "library/ubuntu:latest",
			wantErr: false,
		},
		{
			name:    "tag with custom registry",
			tag:     "gcr.io/my-project/my-image:v1.0.0",
			want:    "my-project/my-image:v1.0.0",
			wantErr: false,
		},
		{
			name:    "tag with port in registry",
			tag:     "localhost:5000/my-image:latest",
			want:    "my-image:latest",
			wantErr: false,
		},
		{
			name:    "invalid tag format",
			tag:     ":::invalid",
			want:    "",
			wantErr: true,
		},
		{
			name:    "empty tag",
			tag:     "",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := removeRegistryFromTag(tt.tag)
			if (err != nil) != tt.wantErr {
				t.Errorf("removeRegistryFromTag() error = %v, wantErr %v", err, tt.wantErr)

				return
			}
			if got != tt.want {
				t.Errorf("removeRegistryFromTag() = %v, want %v", got, tt.want)
			}
		})
	}
}
