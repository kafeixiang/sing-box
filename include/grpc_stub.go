//go:build !with_grpc

package include

import (
	"github.com/sagernet/sing-box/adapter/service"
)

func registerAPI(registry *service.Registry) {
}
