package cli

import (
	"runtime/debug"
	"testing"
)

func TestBuildInfoWithEmbedded(t *testing.T) {
	unset := BuildInfo{Version: UnknownVersion, Commit: UnknownCommit, Date: UnknownDate}
	vcs := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"},
		{Key: "vcs.time", Value: "2026-01-15T10:00:00Z"},
		{Key: "vcs.modified", Value: "false"},
	}
	dirty := append(append([]debug.BuildSetting{}, vcs[:2]...), debug.BuildSetting{Key: "vcs.modified", Value: "true"})

	tests := []struct {
		name  string
		start BuildInfo
		info  *debug.BuildInfo
		want  BuildInfo
	}{
		{"go install of a tag: version only",
			unset, &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}},
			BuildInfo{"1.2.3", UnknownCommit, UnknownDate}},
		{"go build in a checkout at a tag",
			unset, &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}, Settings: vcs},
			BuildInfo{"1.2.3", "a1b2c3d", "2026-01-15T10:00:00Z"}},
		{"go build with uncommitted changes",
			unset, &debug.BuildInfo{Main: debug.Module{Version: "v1.2.4-0.20260115100000-a1b2c3d4e5f6+dirty"}, Settings: dirty},
			BuildInfo{"1.2.4-0.20260115100000-a1b2c3d4e5f6+dirty", "a1b2c3d-dirty", "2026-01-15T10:00:00Z"}},
		{"no version recorded (go run, go test)",
			unset, &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs},
			BuildInfo{UnknownVersion, "a1b2c3d", "2026-01-15T10:00:00Z"}},
		{"nothing recorded",
			unset, &debug.BuildInfo{}, unset},
		{"no build info",
			unset, nil, unset},
		{"link-time values win",
			BuildInfo{"9.9.9", "fff0000", "release-day"}, &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}, Settings: vcs},
			BuildInfo{"9.9.9", "fff0000", "release-day"}},
		{"only the unset fields are filled",
			BuildInfo{"dev-fff0000", UnknownCommit, UnknownDate}, &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}, Settings: vcs},
			BuildInfo{"dev-fff0000", "a1b2c3d", "2026-01-15T10:00:00Z"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.start.WithEmbedded(tt.info); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
