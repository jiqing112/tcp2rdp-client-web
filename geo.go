package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	geoCacheTTL     = 30 * 24 * time.Hour
	geoQueryTimeout = 4 * time.Second
	geoMaxBody      = 64 << 10
)

// GeoInfo is where a node lives: the address its server name resolves to, and
// the country that address belongs to.
type GeoInfo struct {
	IP          string `json:"ip"`
	CountryCode string `json:"countryCode"`
}

type geoEntry struct {
	Code string `json:"code"`
	At   int64  `json:"at"`
}

// geoCache is the lookup table behind the flags on the node rows: one answer
// per server address, cached in the data directory for a month.
type geoCache struct {
	mu      sync.Mutex
	loaded  bool
	entries map[string]geoEntry
	path    string
}

func newGeoCache(dataDir string) *geoCache {
	return &geoCache{path: filepath.Join(dataDir, "geo-cache.json")}
}

func (g *geoCache) Locate(server string) (GeoInfo, error) {
	host, _, ok := splitHostPort(normalizeServer(server))
	if !ok || host == "" {
		return GeoInfo{}, errors.New("服务器地址无效")
	}
	ip, err := resolveIPv4(host)
	if err != nil {
		return GeoInfo{}, err
	}
	if !publicIP(ip) {
		return GeoInfo{IP: ip}, nil
	}
	if code := g.lookup(ip); code != "" {
		return GeoInfo{IP: ip, CountryCode: code}, nil
	}
	code, err := queryCountry(ip)
	if err != nil {
		return GeoInfo{IP: ip}, err
	}
	g.store(ip, code)
	return GeoInfo{IP: ip, CountryCode: code}, nil
}

func resolveIPv4(host string) (string, error) {
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip.String(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", fmt.Errorf("解析 %s 失败：%w", host, err)
	}
	for _, a := range addrs {
		if v4 := a.IP.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	if len(addrs) > 0 {
		return addrs[0].IP.String(), nil
	}
	return "", fmt.Errorf("解析 %s 没有得到地址", host)
}

func publicIP(raw string) bool {
	ip := net.ParseIP(raw)
	if ip == nil {
		return false
	}
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
}

func (g *geoCache) lookup(ip string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loadLocked()
	e, ok := g.entries[ip]
	if !ok || time.Since(time.Unix(e.At, 0)) > geoCacheTTL {
		return ""
	}
	return e.Code
}

func (g *geoCache) loadLocked() {
	if g.loaded {
		return
	}
	g.loaded = true
	g.entries = map[string]geoEntry{}
	data, err := os.ReadFile(g.path)
	if err != nil {
		return
	}
	var file struct {
		Entries map[string]geoEntry `json:"entries"`
	}
	if json.Unmarshal(data, &file) != nil {
		return
	}
	for ip, e := range file.Entries {
		if validCountry(e.Code) {
			g.entries[ip] = e
		}
	}
}

func (g *geoCache) store(ip, code string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loadLocked()
	now := time.Now()
	g.entries[ip] = geoEntry{Code: code, At: now.Unix()}
	for k, e := range g.entries {
		if now.Sub(time.Unix(e.At, 0)) > geoCacheTTL {
			delete(g.entries, k)
		}
	}
	data, err := json.MarshalIndent(struct {
		Entries map[string]geoEntry `json:"entries"`
	}{Entries: g.entries}, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(g.path), 0o755); err != nil {
		return
	}
	tmp := g.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, g.path)
}

// queryCountry tries independent services in turn.
func queryCountry(ip string) (string, error) {
	client := &http.Client{
		Timeout: geoQueryTimeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   3 * time.Second,
			ResponseHeaderTimeout: 3 * time.Second,
		},
	}
	escaped := url.PathEscape(ip)
	providers := []struct {
		name string
		url  string
		read func([]byte) string
	}{
		{
			name: "country.is",
			url:  "https://api.country.is/" + escaped,
			read: func(b []byte) string {
				var v struct {
					Country string `json:"country"`
				}
				_ = json.Unmarshal(b, &v)
				return v.Country
			},
		},
		{
			name: "ipwho.is",
			url:  "https://ipwho.is/" + escaped + "?fields=success,country_code",
			read: func(b []byte) string {
				var v struct {
					Success     bool   `json:"success"`
					CountryCode string `json:"country_code"`
				}
				_ = json.Unmarshal(b, &v)
				if !v.Success {
					return ""
				}
				return v.CountryCode
			},
		},
		{
			name: "ip.sb",
			url:  "https://api.ip.sb/geoip/" + escaped,
			read: func(b []byte) string {
				var v struct {
					CountryCode string `json:"country_code"`
				}
				_ = json.Unmarshal(b, &v)
				return v.CountryCode
			},
		},
	}

	var lastErr error
	for _, p := range providers {
		code, err := geoGet(client, p.url, p.read)
		if err == nil {
			return code, nil
		}
		lastErr = fmt.Errorf("%s：%w", p.name, err)
	}
	return "", lastErr
}

func geoGet(client *http.Client, endpoint string, read func([]byte) string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tcp2rdp-workbench")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, geoMaxBody))
	if err != nil {
		return "", err
	}
	code := strings.ToUpper(strings.TrimSpace(read(data)))
	if !validCountry(code) {
		return "", errors.New("没有返回国家代码")
	}
	return code, nil
}

func validCountry(code string) bool {
	if len(code) != 2 {
		return false
	}
	for _, c := range code {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}
