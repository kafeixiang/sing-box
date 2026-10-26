package constant

import (
	"runtime/debug"
	"strings"
)

var Version = "unknown"

func init() {
	if Version == "" || Version == "unknown" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" && setting.Value != "" {
					rev := setting.Value
					if len(rev) > 8 {
						rev = rev[:8]
					}
					Version = rev
					return
				}
			}
			for _, dep := range info.Deps {
				if dep.Path == "github.com/sagernet/sing-box" && dep.Version != "" && dep.Version != "(devel)" {
					Version = strings.TrimPrefix(dep.Version, "v")
					return
				}
			}
		}
	}
}
