package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr []string
	}{
		{
			name:       "引数なしは使い方を出す",
			args:       nil,
			wantCode:   exitUsage,
			wantStderr: []string{"使い方: tencle"},
		},
		{
			name:       "不明なサブコマンドは名前と使い方を出す",
			args:       []string{"nope"},
			wantCode:   exitUsage,
			wantStderr: []string{`不明なサブコマンドです: "nope"`, "使い方: tencle"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer
			code := run(tt.args, &stderr)

			if code != tt.wantCode {
				t.Errorf("終了コード = %d, want %d", code, tt.wantCode)
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("標準エラー = %q, want %q を含む", stderr.String(), want)
				}
			}
		})
	}
}
