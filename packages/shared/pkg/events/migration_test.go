package events

import "testing"

func TestLegacySandboxEventMigrationMapping(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   SandboxEvent
		want SandboxEvent
	}{
		"v1-created": {
			in: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "create",
			},
			want: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "create",
				Type:          "sandbox.lifecycle.created",
				Version:       "v1",
			},
		},
		"v1-paused": {
			in: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "pause",
			},
			want: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "pause",
				Type:          "sandbox.lifecycle.paused",
				Version:       "v1",
			},
		},
		"v1-killed": {
			in: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "kill",
			},
			want: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "kill",
				Type:          "sandbox.lifecycle.killed",
				Version:       "v1",
			},
		},
		"v1-resumed": {
			in: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "resume",
			},
			want: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "resume",
				Type:          "sandbox.lifecycle.resumed",
				Version:       "v1",
			},
		},
		"v1-updated": {
			in: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "update",
			},
			want: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "update",
				Type:          "sandbox.lifecycle.updated",
				Version:       "v1",
			},
		},
		"v1-custom": {
			in: SandboxEvent{
				Type:    "sandbox.custom",
				Version: "v1",
			},
			want: SandboxEvent{
				Type:    "sandbox.custom",
				Version: "v1",
			},
		},
		"v2-created": {
			in: SandboxEvent{
				Version: "v2",
				Type:    "sandbox.lifecycle.created",
			},
			want: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "create",
				Type:          "sandbox.lifecycle.created",
				Version:       "v2",
			},
		},
		"v2-paused": {
			in: SandboxEvent{
				Version: "v2",
				Type:    "sandbox.lifecycle.paused",
			},
			want: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "pause",
				Type:          "sandbox.lifecycle.paused",
				Version:       "v2",
			},
		},
		"v2-killed": {
			in: SandboxEvent{
				Version: "v2",
				Type:    "sandbox.lifecycle.killed",
			},
			want: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "kill",
				Type:          "sandbox.lifecycle.killed",
				Version:       "v2",
			},
		},
		"v2-resumed": {
			in: SandboxEvent{
				Version: "v2",
				Type:    "sandbox.lifecycle.resumed",
			},
			want: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "resume",
				Type:          "sandbox.lifecycle.resumed",
				Version:       "v2",
			},
		},
		"v2-updated": {
			in: SandboxEvent{
				Version: "v2",
				Type:    "sandbox.lifecycle.updated",
			},
			want: SandboxEvent{
				EventCategory: "lifecycle",
				EventLabel:    "update",
				Type:          "sandbox.lifecycle.updated",
				Version:       "v2",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := LegacySandboxEventMigrationMapping(tc.in)
			if got.Version != tc.want.Version {
				t.Fatalf("Version: got %q, want %q", got.Version, tc.want.Version)
			}

			if got.Type != tc.want.Type {
				t.Fatalf("Event: got %q, want %q (category=%q, label=%q)",
					got.Type, tc.want.Type, tc.in.EventCategory, tc.in.EventLabel)
			}

			if got.EventCategory != tc.want.EventCategory || got.EventLabel != tc.want.EventLabel {
				t.Fatalf("EventCategory/EventLabel mutated: before (%q,%q) after (%q,%q)",
					tc.in.EventCategory, tc.in.EventLabel, got.EventCategory, got.EventLabel)
			}
		})
	}
}
