//go:build !windows

package clash

import (
	"context"
	"errors"
	"net"
)

func dialPipe(context.Context, string) (net.Conn, error) {
	return nil, errors.New("命名管道仅在 Windows 上可用")
}
