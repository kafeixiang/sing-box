package grpc_server

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"grpc_server/auth"
	"grpc_server/gen"
	"log"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/matsuridayo/libneko/neko_common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type BaseServer struct {
	gen.LibcoreServiceServer
}

func (s *BaseServer) Exit(ctx context.Context, in *gen.EmptyReq) (out *gen.EmptyResp, _ error) {
	out = &gen.EmptyResp{}

	// Connection closed
	os.Exit(0)
	return
}

// UnaryInterceptor for authentication
func authUnaryInterceptor(authenticator auth.Authenticator) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		newCtx, err := authenticator.Authenticate(ctx)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "authentication failed: %v", err)
		}
		return handler(newCtx, req)
	}
}

// StreamInterceptor for authentication
func authStreamInterceptor(authenticator auth.Authenticator) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		newCtx, err := authenticator.Authenticate(ss.Context())
		if err != nil {
			return status.Errorf(codes.Unauthenticated, "authentication failed: %v", err)
		}

		// Create a new wrapped stream with the authenticated context
		wrappedStream := &wrappedServerStream{
			ServerStream: ss,
			ctx:          newCtx,
		}
		return handler(srv, wrappedStream)
	}
}

// wrappedServerStream is a custom ServerStream that overrides Context()
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

func RunCore(setupCore func(), server gen.LibcoreServiceServer) {
	_token := flag.String("token", "", "")
	_port := flag.Int("port", 19810, "")
	_debug := flag.Bool("debug", false, "")
	flag.CommandLine.Parse(os.Args[2:])

	neko_common.Debug = *_debug

	go func() {
		parent, err := os.FindProcess(os.Getppid())
		if err != nil {
			log.Fatalln("find parent:", err)
		}
		if runtime.GOOS == "windows" {
			state, err := parent.Wait()
			log.Fatalln("parent exited:", state, err)
		} else {
			for {
				time.Sleep(time.Second * 10)
				err = parent.Signal(syscall.Signal(0))
				if err != nil {
					log.Fatalln("parent exited:", err)
				}
			}
		}
	}()

	// Libcore
	setupCore()

	// GRPC
	lis, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(*_port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	token := *_token
	if token == "" {
		os.Stderr.WriteString("Please set a token: ")
		s := bufio.NewScanner(os.Stdin)
		if s.Scan() {
			token = strings.TrimSpace(s.Text())
		}
	}
	if token == "" {
		fmt.Println("You must set a token")
		os.Exit(0)
	}
	os.Stderr.WriteString("token is set\n")

	auther := auth.Authenticator{
		Token: token,
	}

	s := grpc.NewServer(
		grpc.StreamInterceptor(authStreamInterceptor(auther)),
		grpc.UnaryInterceptor(authUnaryInterceptor(auther)),
	)
	gen.RegisterLibcoreServiceServer(s, server)

	name := "nekoray_core"
	if neko_common.RunMode == neko_common.RunMode_NekoBox_Core {
		name = "nekobox_core"
	}

	log.Printf("%s grpc server listening at %v\n", name, lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
