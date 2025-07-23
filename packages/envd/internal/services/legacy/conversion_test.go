package legacy

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

func TestConversion(t *testing.T) {
	testCases := []struct {
		name     string
		input    connect.AnyResponse
		expected connect.AnyResponse
	}{
		{
			name: "MoveResponse with populated fields",
			input: connect.NewResponse(&filesystem.MoveResponse{
				Entry: &filesystem.EntryInfo{
					Name: "test.txt",
					Type: filesystem.FileType_FILE_TYPE_FILE,
					Path: "/test/test.txt",
				},
			}),
			expected: connect.NewResponse(&MoveResponse{
				Entry: &EntryInfo{
					Name: "test.txt",
					Type: FileType_FILE_TYPE_FILE,
					Path: "/test/test.txt",
				},
			}),
		},
		{
			name:     "MoveResponse with nil fields",
			input:    connect.NewResponse(&filesystem.MoveResponse{}),
			expected: connect.NewResponse(&MoveResponse{}),
		},
		{
			name: "ListDirResponse with populated fields",
			input: connect.NewResponse(&filesystem.ListDirResponse{
				Entries: []*filesystem.EntryInfo{
					{
						Name: "test1.txt",
						Type: filesystem.FileType_FILE_TYPE_FILE,
						Path: "/test/test1.txt",
					},
					{
						Name: "test2.txt",
						Type: filesystem.FileType_FILE_TYPE_FILE,
						Path: "/test/test2.txt",
					},
				},
			}),
			expected: connect.NewResponse(&ListDirResponse{
				Entries: []*EntryInfo{
					{
						Name: "test1.txt",
						Type: FileType_FILE_TYPE_FILE,
						Path: "/test/test1.txt",
					},
					{
						Name: "test2.txt",
						Type: FileType_FILE_TYPE_FILE,
						Path: "/test/test2.txt",
					},
				},
			}),
		},
		{
			name:     "ListDirResponse with nil fields",
			input:    connect.NewResponse(&filesystem.ListDirResponse{}),
			expected: connect.NewResponse(&ListDirResponse{}),
		},
		{
			name: "MakeDirResponse with populated fields",
			input: connect.NewResponse(&filesystem.MakeDirResponse{
				Entry: &filesystem.EntryInfo{
					Name: "testdir",
					Type: filesystem.FileType_FILE_TYPE_DIRECTORY,
					Path: "/test/testdir",
				},
			}),
			expected: connect.NewResponse(&MakeDirResponse{
				Entry: &EntryInfo{
					Name: "testdir",
					Type: FileType_FILE_TYPE_DIRECTORY,
					Path: "/test/testdir",
				},
			}),
		},
		{
			name:     "MakeDirResponse with nil fields",
			input:    connect.NewResponse(&filesystem.MakeDirResponse{}),
			expected: connect.NewResponse(&MakeDirResponse{}),
		},
		{
			name:     "RemoveResponse",
			input:    connect.NewResponse(&filesystem.RemoveResponse{}),
			expected: connect.NewResponse(&RemoveResponse{}),
		},
		{
			name: "StatResponse with populated fields",
			input: connect.NewResponse(&filesystem.StatResponse{
				Entry: &filesystem.EntryInfo{
					Name: "test.txt",
					Type: filesystem.FileType_FILE_TYPE_FILE,
					Path: "/test/test.txt",
				},
			}),
			expected: connect.NewResponse(&StatResponse{
				Entry: &EntryInfo{
					Name: "test.txt",
					Type: FileType_FILE_TYPE_FILE,
					Path: "/test/test.txt",
				},
			}),
		},
		{
			name:     "StatResponse with nil fields",
			input:    connect.NewResponse(&filesystem.StatResponse{}),
			expected: connect.NewResponse(&StatResponse{}),
		},
		{
			name: "WatchDirResponse with Start event",
			input: connect.NewResponse(&filesystem.WatchDirResponse{
				Event: &filesystem.WatchDirResponse_Start{
					Start: &filesystem.WatchDirResponse_StartEvent{},
				},
			}),
			expected: connect.NewResponse(&WatchDirResponse{
				Event: &WatchDirResponse_Start{
					Start: &WatchDirResponse_StartEvent{},
				},
			}),
		},
		{
			name: "WatchDirResponse with Filesystem event",
			input: connect.NewResponse(&filesystem.WatchDirResponse{
				Event: &filesystem.WatchDirResponse_Filesystem{
					Filesystem: &filesystem.FilesystemEvent{
						Name: "test.txt",
						Type: filesystem.EventType_EVENT_TYPE_CREATE,
					},
				},
			}),
			expected: connect.NewResponse(&WatchDirResponse{
				Event: &WatchDirResponse_Filesystem{
					Filesystem: &FilesystemEvent{
						Name: "test.txt",
						Type: EventType_EVENT_TYPE_CREATE,
					},
				},
			}),
		},
		{
			name: "WatchDirResponse with Keepalive event",
			input: connect.NewResponse(&filesystem.WatchDirResponse{
				Event: &filesystem.WatchDirResponse_Keepalive{
					Keepalive: &filesystem.WatchDirResponse_KeepAlive{},
				},
			}),
			expected: connect.NewResponse(&WatchDirResponse{
				Event: &WatchDirResponse_Keepalive{
					Keepalive: &WatchDirResponse_KeepAlive{},
				},
			}),
		},
		{
			name:     "WatchDirResponse with nil event",
			input:    connect.NewResponse(&filesystem.WatchDirResponse{}),
			expected: connect.NewResponse(&WatchDirResponse{}),
		},
		{
			name: "CreateWatcherResponse with populated fields",
			input: connect.NewResponse(&filesystem.CreateWatcherResponse{
				WatcherId: "test-watcher-id",
			}),
			expected: connect.NewResponse(&CreateWatcherResponse{
				WatcherId: "test-watcher-id",
			}),
		},
		{
			name:     "CreateWatcherResponse with empty fields",
			input:    connect.NewResponse(&filesystem.CreateWatcherResponse{}),
			expected: connect.NewResponse(&CreateWatcherResponse{}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := maybeConvertResponse(zerolog.DefaultContextLogger, tc.input)

			expectedMsg := tc.expected.Any()
			resultMsg := actual.Any()

			assert.Equal(t, expectedMsg, resultMsg)
		})
	}
}

func TestConvertValue(t *testing.T) {
	testCases := map[string]struct {
		input, expected any
	}{
		"pass through for unknown values": {
			input:    25,
			expected: 25,
		},

		"move response without value": {
			input: &filesystem.MoveResponse{
				Entry: &filesystem.EntryInfo{
					Name: "test.txt",
					Type: filesystem.FileType_FILE_TYPE_FILE,
					Path: "/test/test.txt",
				},
			},
			expected: &MoveResponse{
				Entry: &EntryInfo{
					Name: "test.txt",
					Type: FileType_FILE_TYPE_FILE,
					Path: "/test/test.txt",
				},
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			actual := maybeConvertValue(tc.input)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
