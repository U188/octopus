package relay

import "testing"

func TestInferResponsesToolPermissionDeny(t *testing.T) {
	tool, reason := inferResponsesToolPermissionDeny(
		[]string{"web_search", "image_generation"},
		`{"error":{"message":"Image generation is not enabled for this group","type":"permission_error"}}`,
	)
	if tool != "image_generation" {
		t.Fatalf("expected image_generation, got %q (%s)", tool, reason)
	}

	tool, _ = inferResponsesToolPermissionDeny(
		[]string{"web_search"},
		`{"error":{"message":"Upstream rate limit exceeded","type":"rate_limit_error"}}`,
	)
	if tool != "" {
		t.Fatalf("rate limit should not auto deny a tool, got %q", tool)
	}

	tool, reason = inferResponsesToolPermissionDeny(
		[]string{"image_generation"},
		`{"error":{"message":"The channel is temporarily unavailable. Please contact the administrator.","type":"permission_error"}}`,
	)
	if tool != "image_generation" {
		t.Fatalf("expected single permission-error tool image_generation, got %q (%s)", tool, reason)
	}
}
