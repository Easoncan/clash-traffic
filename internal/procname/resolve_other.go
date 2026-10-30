//go:build !darwin

package procname

import (
	"context"

	"clashtraffic/internal/clash"
)

// Resolver supplements missing core metadata on supported platforms.
type Resolver struct{}

func (*Resolver) Resolve(context.Context, []clash.Conn) {}
