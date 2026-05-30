package dbcerts

import (
	"context"
	"testing"
)

func TestIssueValidation(t *testing.T) {
	// nil client
	if _, err := Issue(context.Background(), nil, Request{Database: "db"}); err == nil {
		t.Fatal("expected error for nil client")
	}
}
