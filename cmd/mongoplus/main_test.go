package main

import (
	"bytes"
	"testing"
)

func TestCLIRejectsInvalidCommands(t *testing.T) {
	for _, args := range [][]string{{"unknown"}, {"schema"}, {"schema", "drop"}, {"generate", "--typo"}, {"generate", "extra"}, {"check", "--timeout=-1s"}} {
		var out bytes.Buffer
		if err := run(args, &out, &out); err == nil {
			t.Fatalf("允许无效命令 %v", args)
		}
	}
	var out bytes.Buffer
	if err := run([]string{"help"}, &out, &out); err != nil || out.Len() == 0 {
		t.Fatal("缺少帮助")
	}
}
