//go:build !tun

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/containers/winquit/pkg/winquit"
	"github.com/mythologyli/zju-connect/client"
	atrustclient "github.com/mythologyli/zju-connect/client/atrust"
	"github.com/mythologyli/zju-connect/client/atrust/auth"
	"github.com/mythologyli/zju-connect/client/authchallenge"
	"github.com/mythologyli/zju-connect/configs"
	"github.com/mythologyli/zju-connect/dial"
	"github.com/mythologyli/zju-connect/internal/keylog"
	"github.com/mythologyli/zju-connect/log"
	"github.com/mythologyli/zju-connect/resolve"
	"github.com/mythologyli/zju-connect/routing"
	"github.com/mythologyli/zju-connect/service"
	"github.com/mythologyli/zju-connect/stack/gvisor"
	"github.com/mythologyli/zju-connect/underlay"
)

func runSelective(c configs.Config) error {
	if err := routing.Validate(c); err != nil {
		return err
	}
	router, err := routing.New(c.Routing)
	if err != nil {
		return err
	}
	defer router.Close()
	u, err := underlay.New(underlay.Options{InterfaceName: c.BindInterface, AutoDetect: c.AutoDetectInterface, DebugPCAPFile: c.DebugPCAPFile, LocalDNSServer: c.LocalDNSServer})
	if err != nil {
		return err
	}
	defer u.Close()
	router.SetControl(u.DialContext, []string{net.JoinHostPort(c.ServerAddress, strconv.Itoa(c.ServerPort)), net.JoinHostPort(c.LoginDomain, "443")}, []string{c.SocksBind, c.HTTPBind})
	done := make(chan os.Signal, 1)
	if runtime.GOOS == "windows" {
		signal.Notify(done, syscall.SIGINT)
		winquit.SimulateSigTermOnQuit(done)
	} else {
		signal.Notify(done, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	}
	defer signal.Stop(done)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopTraffic := startSelectiveTraffic(ctx, router)
	defer stopTraffic()
	go func() {
		select {
		case <-done:
			cancel()
		case <-authchallenge.GUIStop():
			cancel()
		case <-ctx.Done():
		}
	}()
	if c.Routing.RulesFile != "" {
		watcher, err := routing.WatchRulesFile(ctx, c.Routing.RulesFile, router)
		if err != nil {
			return fmt.Errorf("start routing rule file: %w", err)
		}
		defer watcher.Close()
	}
	var sessionData selectiveSessionData
	return serveSelectiveRecovery(ctx, c, router.Dial, func(ctx context.Context, mode selectiveSessionMode, ready func()) error {
		return runSelectiveVPNWithMode(ctx, c, u, router, mode, ready, &sessionData)
	})
}

func startSelectiveTraffic(parent context.Context, router *routing.Router) func() {
	if os.Getenv("FDU_CONNECT_GUI") != "1" {
		return func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	authchallenge.GUITraffic(router.Traffic())
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				authchallenge.GUITraffic(router.Traffic())
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
			// The proxy listeners finish before this deferred stop. Flush the
			// last partial second before the owning GUI drains stdout.
			authchallenge.GUITraffic(router.Traffic())
		})
	}
}

func serveSelective(ctx context.Context, c configs.Config, dial client.DialContextFunc, connect func(context.Context) error) error {
	return serveSelectiveRecovery(ctx, c, dial, func(ctx context.Context, _ selectiveSessionMode, _ func()) error { return connect(ctx) })
}

func serveSelectiveRecovery(ctx context.Context, c configs.Config, dial client.DialContextFunc, connect selectiveConnectFunc) error {
	proxies, err := service.StartRoutingProxies(ctx, c, dial)
	if err != nil {
		return err
	}
	defer proxies.Close()
	log.Printf("Selective router ready: SOCKS=%s HTTP=%s; ordinary traffic uses the configured Clash upstream", proxies.SocksAddr(), proxies.HTTPAddr())
	runSelectiveSessions(ctx, connect, authchallenge.GUIRelogin(), authchallenge.GUIState, time.Second)
	return nil
}

// This opt-in path is managed independently of the legacy fatal-on-disconnect
// startup. All resources stay local to this lifecycle, not global shutdown hooks.
func runSelectiveVPN(ctx context.Context, c configs.Config, u *underlay.Dialer, router *routing.Router) error {
	return runSelectiveVPNWithMode(ctx, c, u, router, selectiveInitial, func() {}, &selectiveSessionData{})
}

type selectiveSessionData struct {
	clientData   []byte
	username     string
	freshPending bool
}

func runSelectiveVPNWithMode(ctx context.Context, c configs.Config, u *underlay.Dialer, router *routing.Router, mode selectiveSessionMode, onReady func(), sessionData *selectiveSessionData) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if mode == selectiveFresh {
		sessionData.freshPending = true
	} else if mode == selectiveResume && sessionData.freshPending {
		return auth.ErrSessionInvalid
	}
	tlsWriter, err := keylog.Open(c.DebugTLSLogFile)
	if err != nil {
		return err
	}
	if tlsWriter != nil {
		defer tlsWriter.Close()
	}
	var resourceData, clientData []byte
	if c.ResourceFile != "" {
		resourceData, err = os.ReadFile(c.ResourceFile)
		if err != nil {
			return fmt.Errorf("read resource file: %w", err)
		}
	}
	if sessionData.clientData != nil {
		clientData = append([]byte(nil), sessionData.clientData...)
		c.SID, c.SignKey = "", ""
		c.Username = sessionData.username
	} else if c.ClientDataFile != "" {
		clientData, err = os.ReadFile(c.ClientDataFile)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read client data: %w", err)
		}
	}
	setupOptions, err := aTrustSetupOptionsForSession(c, resourceData, clientData, mode == selectiveFresh, mode == selectiveResume)
	if err != nil {
		return err
	}
	saveClientData := setupOptions.SaveClientData
	setupOptions.SaveClientData = func(data []byte) error {
		if saveClientData != nil {
			if err := saveClientData(data); err != nil {
				return err
			}
		}
		sessionData.clientData = append([]byte(nil), data...)
		sessionData.freshPending = false
		return nil
	}
	invalid := make(chan error, 1)
	var ready atomic.Bool
	vpn := atrustclient.NewClient(atrustclient.ClientOptions{
		Session: atrustclient.SessionOptions{Username: c.Username, SID: c.SID, DeviceID: c.DeviceID, SignKey: c.SignKey}, UnderlayDialer: u, TLSKeyLogWriter: tlsWriter,
		OnSessionInvalid: func(err error) {
			select {
			case invalid <- err:
			default:
			}
		},
		OnConnectionState: func(connected bool) {
			if !ready.Load() || ctx.Err() != nil {
				return
			}
			if connected {
				authchallenge.GUIState("connected")
			} else {
				authchallenge.GUIState("reconnecting")
			}
		},
	})
	defer func() { cancel(); vpn.Close() }()
	setupOptions.OnAuthenticated = func(data []byte) {
		sessionData.clientData = append([]byte(nil), data...)
		sessionData.username = vpn.Username
		sessionData.freshPending = false
	}
	_, err = vpn.SetupContext(ctx, setupOptions)
	if err != nil {
		return err
	}
	sessionData.username = vpn.Username
	ips, _ := vpn.IPResources()
	domains, _ := vpn.DomainResources()
	dnsResources, _ := vpn.DNSResource()
	s, err := gvisor.NewStack(vpn)
	if err != nil {
		return err
	}
	failed, err := s.Start(ctx)
	if err != nil {
		return err
	}
	remote := c.RemoteDNSServer
	useRemote := !c.DisableRemoteDNS
	if remote == "auto" {
		remote, err = vpn.DNSServer()
		if err != nil {
			useRemote = false
			log.Printf("Campus DNS unavailable: %v", err)
		}
	}
	resolver := resolve.NewVPNResolver(s, remote, c.DNSTTL, domains, dnsResources, useRemote)
	defer resolver.Close()
	for _, entry := range c.CustomDNSList {
		ip := net.ParseIP(entry.IP)
		if ip == nil {
			return fmt.Errorf("invalid custom DNS IP for %s", entry.HostName)
		}
		resolver.SetPermanentDNS(entry.HostName, ip)
	}
	vpnDialer := dial.NewDialer(s, resolver, ips, false, "")
	router.SetVPN(func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := vpnDialer.DialVPN(ctx, network, addr)
		if errors.Is(err, auth.ErrSessionInvalid) {
			// Only the session owner clears the route; an older dial may
			// finish after a replacement session has already been installed.
			select {
			case invalid <- err:
			default:
			}
		}
		return conn, err
	})
	defer router.SetVPN(nil)
	log.Println("Campus VPN ready; only configured destinations use FDU")
	ready.Store(true)
	onReady()
	vpn.PublishConnectionState()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-invalid:
		return err
	case err := <-failed:
		return err
	}
}
