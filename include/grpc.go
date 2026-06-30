//go:build with_grpc

package include

import (
	"github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/service/api"
)

func registerAPI(registry *service.Registry) {
	api.RegisterService(registry)
}
