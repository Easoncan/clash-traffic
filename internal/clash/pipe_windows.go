package clash

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

func dialPipe(ctx context.Context, name string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, name)
}
