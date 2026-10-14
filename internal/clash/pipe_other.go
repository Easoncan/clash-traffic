//go:build !windows

package clash

import (
	"context"
	"errors"
	"net"

	"clashtraffic/internal/i18n"
)

func dialPipe(context.Context, string) (net.Conn, error) {
	return nil, errors.New(i18n.T("Named pipes are only on Windows"))
}
