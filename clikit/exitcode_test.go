package clikit

import (
	"context"
	"testing"
)

func TestScanFlagValueAndNotifyContext(t *testing.T) {
	args := []string{"--config", "/tmp/a.toml", "--ui-lang=zh", "search", "--", "--ui-lang", "en"}
	if got := ScanFlagValue(args, "config"); got != "/tmp/a.toml" {
		t.Fatalf("ScanFlagValue(config) = %q, want /tmp/a.toml", got)
	}
	if got := ScanFlagValue(args, "--ui-lang"); got != "zh" {
		t.Fatalf("ScanFlagValue(--ui-lang) = %q, want zh (should stop at --)", got)
	}
	if got := ScanFlagValue(args, ""); got != "" {
		t.Fatalf("ScanFlagValue(empty) = %q, want empty", got)
	}

	ctx, stop := NotifyContext(context.Background())
	defer stop()
	if ctx.Err() != nil {
		t.Fatalf("unexpected canceled context: %v", ctx.Err())
	}
}
