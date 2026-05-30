package sshcerts

import (
	"context"
	"testing"
)

func TestIssueValidation(t *testing.T) {
	if _, err := Issue(context.Background(), nil, Request{NodeName: "n", SSHLogin: "u"}); err == nil {
		t.Fatal("expected error for nil client")
	}
}
