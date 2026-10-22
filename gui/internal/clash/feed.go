package clash

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/mythologyli/zju-connect/gui/internal/fsx"
)

// The service core has its own home directory. Give it rule contents over
// loopback instead of asking it to read the GUI user's absolute file path.
type campusFeed struct {
	Address string `json:"address"`
	Token   string `json:"token"`
	server  *http.Server
	files   sync.Map
	state   string
	saved   bool
}

func (f *campusFeed) base() string { return "http://" + f.Address + "/" + f.Token + "/" }

func (f *campusFeed) url(path string) string {
	id := fsx.Hash([]byte(path)) + ".yaml"
	f.files.Store("/"+f.Token+"/"+id, path)
	return f.base() + id
}

func (f *campusFeed) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, ok := f.files.Load(r.URL.Path)
	if !ok || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	data, err := fsx.ReadBounded(path.(string), 256*1024)
	if err != nil {
		http.Error(w, "rules unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (f *campusFeed) save() error {
	if f.saved {
		return nil
	}
	raw, err := json.Marshal(f)
	if err == nil {
		err = fsx.AtomicWrite(f.state, raw)
	}
	if err == nil {
		f.saved = true
	}
	return err
}

func (c *Connection) campusFeed() (*campusFeed, error) {
	c.feedMu.Lock()
	defer c.feedMu.Unlock()
	if c.feed != nil {
		return c.feed, nil
	}
	f := &campusFeed{state: filepath.Join(c.Dir, "campus-feed.json")}
	raw, err := fsx.ReadBounded(f.state, 4096)
	if err == nil {
		if err = json.Unmarshal(raw, f); err != nil {
			return nil, errors.New("本机规则接口记录损坏，未启用")
		}
		host, port, err := net.SplitHostPort(f.Address)
		n, _ := strconv.Atoi(port)
		token, decodeErr := hex.DecodeString(f.Token)
		if err != nil || host != "127.0.0.1" || n < 1 || n > 65535 || decodeErr != nil || len(token) != 32 {
			return nil, errors.New("本机规则接口记录无效，未启用")
		}
		f.saved = true
	} else if os.IsNotExist(err) {
		var token [32]byte
		if _, err = rand.Read(token[:]); err != nil {
			return nil, err
		}
		f.Address, f.Token = "127.0.0.1:0", hex.EncodeToString(token[:])
	} else {
		return nil, err
	}
	l, err := net.Listen("tcp4", f.Address)
	if err != nil {
		return nil, fmt.Errorf("本机规则接口无法监听，请确认其他 FDU 窗口已退出：%w", err)
	}
	f.Address = l.Addr().String()
	f.server = &http.Server{Handler: f, ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second}
	c.feed = f
	go func() { _ = f.server.Serve(l) }()
	return f, nil
}

func (c *Connection) campusAPI(a CampusAttachment) (API, error) {
	api, err := c.api(a.Root)
	if err != nil {
		return nil, err
	}
	f, err := c.campusFeed()
	if err != nil {
		return nil, err
	}
	f.url(a.ProviderPath)
	return api, nil
}

// Close releases the local rule endpoint after the backend has detached Clash.
func (c *Connection) Close() {
	c.feedMu.Lock()
	defer c.feedMu.Unlock()
	if c.feed != nil {
		_ = c.feed.server.Close()
		c.feed = nil
	}
}
