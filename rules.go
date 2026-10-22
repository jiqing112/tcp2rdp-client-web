package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"sync"
	"time"
)

const (
	// Same upstreams rdp2tcp downloads on its own when no list flag is given.
	defaultRuleIPURL     = "https://raw.githubusercontent.com/Loyalsoldier/geoip/release/text/cn.txt"
	defaultRuleDomainURL = "https://raw.githubusercontent.com/felixonmars/dnsmasq-china-list/master/accelerated-domains.china.conf"

	// GitHub raw is unreachable from many networks; the same repositories are
	// served by jsDelivr, which is usually reachable where raw is not.
	defaultRuleIPMirror     = "https://cdn.jsdelivr.net/gh/Loyalsoldier/geoip@release/text/cn.txt"
	defaultRuleDomainMirror = "https://cdn.jsdelivr.net/gh/felixonmars/dnsmasq-china-list@master/accelerated-domains.china.conf"

	remoteRuleTTL     = 7 * 24 * time.Hour
	remoteRuleMaxSize = 32 << 20

	remoteRuleDialTimeout = 3 * time.Second
	remoteRuleTLSTimeout  = 5 * time.Second
	remoteRuleTimeout     = 20 * time.Second

	// A blocked upstream costs every connect a timeout; remember the failure so
	// switching nodes does not pay it again while the bundle is in use.
	remoteRuleRetryAfter = 10 * time.Minute
)

var (
	remoteRuleFailMu sync.Mutex
	remoteRuleFailAt = map[string]time.Time{}
)

// bundledRuleFile finds a rule list shipped next to the server binary: the exe
// directory, its resources/ and bin/ subdirectories, then the working
// directory.
func bundledRuleFile(name string) string {
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		dirs = append(dirs, dir, filepath.Join(dir, "resources"), filepath.Join(dir, "bin"))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(wd, "resources"), wd)
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		p := filepath.Join(dir, name)
		if seen[p] {
			continue
		}
		seen[p] = true
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// ruleSource turns a node's list choice into the value -rule-ip / -rule-domain
// expects.
func ruleSource(choice, bundledName string) string {
	switch choice {
	case "", RuleBuiltin:
		return bundledRuleFile(bundledName)
	case RuleRemote:
		return ""
	default:
		return choice
	}
}

// prepareRuleSources resolves the node's choices before the client starts.
func prepareRuleSources(node Node, cacheDir string, logf func(string, ...any)) (ip, domain string) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ip = prepareRuleSource(node.RuleIP, "cn-ip.txt", defaultRuleIPURL, cacheDir, logf)
	}()
	go func() {
		defer wg.Done()
		domain = prepareRuleSource(node.RuleDomain, "cn-domain.txt", defaultRuleDomainURL, cacheDir, logf)
	}()
	wg.Wait()
	return ip, domain
}

func prepareRuleSource(choice, name, url, cacheDir string, logf func(string, ...any)) string {
	switch choice {
	case "", RuleBuiltin:
		return bundledRuleFile(name)
	case RuleRemote:
		sources := []string{url}
		if mirror := ruleMirror(name); mirror != "" && mirror != url {
			sources = append(sources, mirror)
		}
		return remoteRuleFile(name, sources, cacheDir, logf)
	default:
		return choice
	}
}

func ruleMirror(name string) string {
	switch name {
	case "cn-ip.txt":
		return defaultRuleIPMirror
	case "cn-domain.txt":
		return defaultRuleDomainMirror
	}
	return ""
}

func remoteRuleFile(name string, sources []string, cacheDir string, logf func(string, ...any)) string {
	cache := filepath.Join(cacheDir, name)
	info, statErr := os.Stat(cache)
	if statErr == nil && time.Since(info.ModTime()) < remoteRuleTTL {
		return cache
	}

	if !remoteRuleRecentlyFailed(cache) {
		var data []byte
		var err error
		mirror := false
		for i, src := range sources {
			data, err = downloadRule(src)
			if err == nil {
				mirror = i > 0
				break
			}
			if i+1 < len(sources) {
				logf("在线清单主源失败（%v），尝试镜像…", err)
			}
		}
		if err == nil {
			if werr := writeRuleCache(cache, data); werr == nil {
				if mirror {
					logf("在线清单已通过镜像更新：%s（%d 字节）", name, len(data))
				} else {
					logf("在线清单已更新：%s（%d 字节）", name, len(data))
				}
				return cache
			} else {
				logf("在线清单写入缓存失败：%v", werr)
			}
		} else {
			markRemoteRuleFailed(cache)
			logf("在线清单下载失败（%v），本次改用本地清单", err)
		}
	}

	if statErr == nil {
		logf("在线清单使用上次缓存：%s", name)
		return cache
	}
	if bundled := bundledRuleFile(name); bundled != "" {
		logf("在线清单不可用，本次回退到内置清单：%s", name)
		return bundled
	}
	return ""
}

func downloadRule(url string) ([]byte, error) {
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: remoteRuleDialTimeout}).DialContext,
			TLSHandshakeTimeout:   remoteRuleTLSTimeout,
			ResponseHeaderTimeout: remoteRuleTLSTimeout,
			DisableKeepAlives:     true,
		},
		Timeout: remoteRuleTimeout,
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tcp2rdp-workbench")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, remoteRuleMaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("下载到空清单")
	}
	if len(data) > remoteRuleMaxSize {
		return nil, fmt.Errorf("清单超过 %d MB", remoteRuleMaxSize>>20)
	}
	return data, nil
}

func writeRuleCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func remoteRuleRecentlyFailed(cachePath string) bool {
	remoteRuleFailMu.Lock()
	defer remoteRuleFailMu.Unlock()
	at, ok := remoteRuleFailAt[cachePath]
	return ok && time.Since(at) < remoteRuleRetryAfter
}

func markRemoteRuleFailed(cachePath string) {
	remoteRuleFailMu.Lock()
	remoteRuleFailAt[cachePath] = time.Now()
	remoteRuleFailMu.Unlock()
}
