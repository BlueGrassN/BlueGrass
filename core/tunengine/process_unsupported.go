//go:build !windows

package tunengine

import M "github.com/sagernet/sing/common/metadata"

func connectionProcessPath(M.Socksaddr, M.Socksaddr) string { return "" }
