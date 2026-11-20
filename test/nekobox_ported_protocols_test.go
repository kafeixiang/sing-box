package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/netip"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/gofrs/uuid/v5"
	"github.com/stretchr/testify/require"
)

// 1. Test XHTTP Transport with VLESS (stream-one, stream-up, packet-up)
func TestXHTTP_VLESS(t *testing.T) {
	modes := []string{"stream-one", "stream-up", "packet-up"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			testXHTTPMode(t, mode)
		})
	}
}

func testXHTTPMode(t *testing.T, mode string) {
	user, err := uuid.DefaultGenerator.NewV4()
	require.NoError(t, err)

	xhttpOptions := option.V2RayXHTTPOptions{
		Mode: mode,
		V2RayXHTTPBaseOptions: option.V2RayXHTTPBaseOptions{
			Path: "/xhttp-test",
		},
	}

	startInstance(t, option.Options{
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  "mixed-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: clientPort,
					},
				},
			},
			{
				Type: C.TypeVLESS,
				Tag:  "vless-in",
				Options: &option.VLESSInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: serverPort,
					},
					Users: []option.VLESSUser{
						{
							Name: "user1",
							UUID: user.String(),
						},
					},
					Transport: &option.V2RayTransportOptions{
						Type:         C.V2RayTransportTypeXHTTP,
						XHTTPOptions: xhttpOptions,
					},
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
			},
			{
				Type: C.TypeVLESS,
				Tag:  "vless-out",
				Options: &option.VLESSOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					UUID: user.String(),
					Transport: &option.V2RayTransportOptions{
						Type:         C.V2RayTransportTypeXHTTP,
						XHTTPOptions: xhttpOptions,
					},
				},
			},
		},
		Route: &option.RouteOptions{
			Rules: []option.Rule{
				{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultRule{
						RawDefaultRule: option.RawDefaultRule{
							Inbound: []string{"mixed-in"},
						},
						RuleAction: option.RuleAction{
							Action: C.RuleActionTypeRoute,
							RouteOptions: option.RouteActionOptions{
								Outbound: "vless-out",
							},
						},
					},
				},
			},
		},
	})

	testSuit(t, clientPort, testPort)
}

// 2. Test VLESS Encryption
func TestVLESS_Encryption(t *testing.T) {
	testCases := []struct {
		name       string
		xorMode    string
		rtt        string
		serverSecs string
	}{
		{"native-1rtt", "native", "1rtt", "10s-30s"},
		{"native-0rtt", "native", "0rtt", "10s-30s"},
		{"xorpub-1rtt", "xorpub", "1rtt", "10s-30s"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testVLESSEncryption(t, tc.xorMode, tc.rtt, tc.serverSecs)
		})
	}
}

func testVLESSEncryption(t *testing.T, xorMode, rtt, serverSecs string) {
	user, err := uuid.DefaultGenerator.NewV4()
	require.NoError(t, err)

	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	require.NoError(t, err)
	pubKey := privKey.PublicKey()

	privB64 := base64.RawURLEncoding.EncodeToString(privKey.Bytes())
	pubB64 := base64.RawURLEncoding.EncodeToString(pubKey.Bytes())

	decryptionStr := fmt.Sprintf("mlkem768x25519plus.%s.%s.%s", xorMode, serverSecs, privB64)
	encryptionStr := fmt.Sprintf("mlkem768x25519plus.%s.%s.%s", xorMode, rtt, pubB64)

	startInstance(t, option.Options{
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  "mixed-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: clientPort,
					},
				},
			},
			{
				Type: C.TypeVLESS,
				Tag:  "vless-in",
				Options: &option.VLESSInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: serverPort,
					},
					Users: []option.VLESSUser{
						{
							Name: "user1",
							UUID: user.String(),
						},
					},
					Decryption: decryptionStr,
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
			},
			{
				Type: C.TypeVLESS,
				Tag:  "vless-out",
				Options: &option.VLESSOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					UUID:       user.String(),
					Encryption: encryptionStr,
				},
			},
		},
		Route: &option.RouteOptions{
			Rules: []option.Rule{
				{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultRule{
						RawDefaultRule: option.RawDefaultRule{
							Inbound: []string{"mixed-in"},
						},
						RuleAction: option.RuleAction{
							Action: C.RuleActionTypeRoute,
							RouteOptions: option.RouteActionOptions{
								Outbound: "vless-out",
							},
						},
					},
				},
			},
		},
	})

	testSuit(t, clientPort, testPort)
}

// 3. Test mKCP Transport (with VMess and VLESS)
func TestMKCP_Transport(t *testing.T) {
	testCases := []struct {
		name       string
		headerType string
		seed       string
		congestion bool
	}{
		{"none-seed", "none", "kcp-test-seed-123456", false},
		{"wechat-congestion", "wechat-video", "kcp-seed-wechat", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testMKCPVMess(t, tc.headerType, tc.seed, tc.congestion)
		})
	}
}

func testMKCPVMess(t *testing.T, headerType, seed string, congestion bool) {
	user, err := uuid.DefaultGenerator.NewV4()
	require.NoError(t, err)

	kcpOpts := option.V2RayKCPOptions{
		HeaderType: headerType,
		Seed:       seed,
		Congestion: congestion,
	}

	transport := &option.V2RayTransportOptions{
		Type:       C.V2RayTransportTypeKCP,
		KCPOptions: kcpOpts,
	}

	startInstance(t, option.Options{
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  "mixed-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: clientPort,
					},
				},
			},
			{
				Type: C.TypeVMess,
				Tag:  "vmess-in",
				Options: &option.VMessInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: serverPort,
					},
					Users: []option.VMessUser{
						{
							Name: "user1",
							UUID: user.String(),
						},
					},
					Transport: transport,
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
			},
			{
				Type: C.TypeVMess,
				Tag:  "vmess-out",
				Options: &option.VMessOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					UUID:      user.String(),
					Security:  "zero",
					Transport: transport,
				},
			},
		},
		Route: &option.RouteOptions{
			Rules: []option.Rule{
				{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultRule{
						RawDefaultRule: option.RawDefaultRule{
							Inbound: []string{"mixed-in"},
						},
						RuleAction: option.RuleAction{
							Action: C.RuleActionTypeRoute,
							RouteOptions: option.RouteActionOptions{
								Outbound: "vmess-out",
							},
						},
					},
				},
			},
		},
	})

	testTCP(t, clientPort, testPort)
}

// 4. Test Mieru Protocol (TCP & UDP)
func TestMieru_Protocol(t *testing.T) {
	for _, transportProto := range []string{"TCP", "UDP"} {
		t.Run(transportProto, func(t *testing.T) {
			testMieru(t, transportProto)
		})
	}
}

func testMieru(t *testing.T, transportProto string) {
	username := "testmieruuser"
	password := "TestMieruPass123!"

	startInstance(t, option.Options{
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  "mixed-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: clientPort,
					},
				},
			},
			{
				Type: C.TypeMieru,
				Tag:  "mieru-in",
				Options: &option.MieruInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: serverPort,
					},
					Users: []option.MieruUser{
						{
							Name:     username,
							Password: password,
						},
					},
					Transport: transportProto,
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
			},
			{
				Type: C.TypeMieru,
				Tag:  "mieru-out",
				Options: &option.MieruOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					UserName:  username,
					Password:  password,
					Transport: transportProto,
				},
			},
		},
		Route: &option.RouteOptions{
			Rules: []option.Rule{
				{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultRule{
						RawDefaultRule: option.RawDefaultRule{
							Inbound: []string{"mixed-in"},
						},
						RuleAction: option.RuleAction{
							Action: C.RuleActionTypeRoute,
							RouteOptions: option.RouteActionOptions{
								Outbound: "mieru-out",
							},
						},
					},
				},
			},
		},
	})

	testTCP(t, clientPort, testPort)
}

// 5. Test ShadowsocksR (SSR) Outbound with Python ShadowsocksR Server
func TestShadowsocksR_Outbound(t *testing.T) {
	ssrPort := uint16(18388)
	ssrPassword := "TestSSRPass123!"
	ssrMethod := "aes-128-cfb"
	ssrProtocol := "auth_aes128_md5"
	ssrObfs := "http_simple"

	// Start local Python SSR server
	serverScript, err := filepath.Abs("/tmp/shadowsocksr/shadowsocks/server.py")
	require.NoError(t, err)

	cmd := exec.Command("python3", serverScript,
		"-s", "127.0.0.1",
		"-p", fmt.Sprintf("%d", ssrPort),
		"-k", ssrPassword,
		"-m", ssrMethod,
		"-O", ssrProtocol,
		"-o", ssrObfs,
		"--forbidden-ip", "",
	)
	var outputBuf bytes.Buffer
	cmd.Stdout = &outputBuf
	cmd.Stderr = &outputBuf
	require.NoError(t, cmd.Start())
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("Python SSR output:\n%s", outputBuf.String())
		}
	}()

	time.Sleep(500 * time.Millisecond)

	startInstance(t, option.Options{
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  "mixed-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: clientPort,
					},
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
			},
			{
				Type: C.TypeShadowsocksR,
				Tag:  "ssr-out",
				Options: &option.ShadowsocksROutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: ssrPort,
					},
					Method:   ssrMethod,
					Password: ssrPassword,
					Protocol: ssrProtocol,
					Obfs:     ssrObfs,
				},
			},
		},
		Route: &option.RouteOptions{
			Rules: []option.Rule{
				{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultRule{
						RawDefaultRule: option.RawDefaultRule{
							Inbound: []string{"mixed-in"},
						},
						RuleAction: option.RuleAction{
							Action: C.RuleActionTypeRoute,
							RouteOptions: option.RouteActionOptions{
								Outbound: "ssr-out",
							},
						},
					},
				},
			},
		},
	})

	testTCP(t, clientPort, testPort)
}
