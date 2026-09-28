package apperr

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCStatusMapping(t *testing.T) {
	err := GRPCStatus(NotFound("user"))
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.NotFound || st.Message() != "user" {
		t.Fatalf("got %v", err)
	}
	err = GRPCStatus(RateLimited("slow down"))
	st, _ = status.FromError(err)
	if st.Code() != codes.ResourceExhausted {
		t.Fatalf("got %v", st.Code())
	}
}
