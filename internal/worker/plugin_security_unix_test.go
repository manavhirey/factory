//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package worker

import (
	"os"
	"strings"
	"syscall"
	"testing"
)

type fileInfoWithStat struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (info fileInfoWithStat) Sys() any { return info.stat }

func TestValidatePathOwnerRejectsForeignOwnership(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("filesystem does not expose Unix ownership")
	}
	foreign := *stat
	foreign.Uid = uint32(os.Geteuid() + 1)
	err = validatePathOwner(fileInfoWithStat{FileInfo: info, stat: &foreign}, "plugin asset")
	if err == nil || !strings.Contains(err.Error(), "owned by worker uid") {
		t.Fatalf("foreign-owner error = %v", err)
	}
}
