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
	"syscall"

	"github.com/containers/winquit/pkg/winquit"
	"github.com/mythologyli/zju-connect/client"
	atrustclient "github.com/mythologyli/zju-connect/client/atrust"
	"github.com/mythologyli/zju-connect/client/atrust/auth"
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
	go func() {
		select {
		case <-done:
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
	return serveSelective(ctx, c, router.Dial, func(ctx context.Context) error { return runSelectiveVPN(ctx, c, u, router) })
}

func serveSelective(ctx context.Context, c configs.Config, dial client.DialContextFunc, connect func(context.Context) error) error {
	proxies, err := service.StartRoutingProxies(ctx, c, dial)
	if err != nil {
		return err
	}
	defer proxies.Close()
	log.Printf("Selective router ready: SOCKS=%s HTTP=%s; ordinary traffic uses the configured Clash upstream", proxies.SocksAddr(), proxies.HTTPAddr())
	if err = connect(ctx); err != nil && ctx.Err() == nil {
		log.Printf("Campus VPN unavailable: %v; ordinary Internet forwarding remains available", err)
	}
	<-ctx.Done()
	return nil
}

// This opt-in path is managed independently of the legacy fatal-on-disconnect
// startup. All resources stay local to this lifecycle, not global shutdown hooks.
func runSelectiveVPN(ctx context.Context, c configs.Config, u *underlay.Dialer, router *routing.Router) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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
	if c.ClientDataFile != "" {
		clientData, err = os.ReadFile(c.ClientDataFile)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read client data: %w", err)
		}
	}
	setupOptions, err := aTrustSetupOptions(c, resourceData, clientData)
	if err != nil {
		return err
	}
	invalid := make(chan error, 1)
	vpn := atrustclient.NewClient(atrustclient.ClientOptions{
		Session: atrustclient.SessionOptions{Username: c.Username, SID: c.SID, DeviceID: c.DeviceID, SignKey: c.SignKey}, UnderlayDialer: u, TLSKeyLogWriter: tlsWriter,
		OnSessionInvalid: func(err error) {
			select {
			case invalid <- err:
			default:
			}
		},
	})
	defer vpn.Close()
	_, err = vpn.SetupContext(ctx, setupOptions)
	if err != nil {
		return err
	}
	ips, _ := vpn.IPResources()
	domains, _ := vpn.DomainResources()
	dnsResources, _ := vpn.DNSResource()
	s, err := gvisor.NewStack(vpn)
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
	failed, err := s.Start(ctx)
	if err != nil {
		return err
	}
	vpnDialer := dial.NewDialer(s, resolver, ips, false, "")
	router.SetVPN(func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := vpnDialer.DialVPN(ctx, network, addr)
		if errors.Is(err, auth.ErrSessionInvalid) {
			router.SetVPN(nil)
			select {
			case invalid <- err:
			default:
			}
		}
		return conn, err
	})
	defer router.SetVPN(nil)
	log.Println("Campus VPN ready; only configured destinations use FDU")
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-invalid:
		return err
	case err := <-failed:
		return err
	}
}
