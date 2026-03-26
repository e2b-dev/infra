//go:build compression

package api_templates

import "testing"

// Compressed variants of template build tests.
// These run only with -tags compression and exercise the same logic
// as the untagged tests, but against an orchestrator with compression enabled.

func TestCompressTemplateBuildRUN(t *testing.T)     { TestTemplateBuildRUN(t) }
func TestCompressTemplateBuildLayered(t *testing.T) { TestTemplateBuildFromTemplateLayered(t) }
func TestCompressTemplateBuildCache(t *testing.T)   { TestTemplateBuildCache(t) }
