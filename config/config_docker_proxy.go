package config

import (
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/distribution/reference"
)

// DockerProxyConfiguration is the intranet HTTP proxy used for preset image
// pulls and for the installer container. Registry values override the default
// proxy. An empty value or "-" keeps that registry on the Docker daemon, which
// is what still applies daemon.json registry mirrors.
type DockerProxyConfiguration struct {
	HTTP       string            `json:"http" yaml:"http"`
	HTTPS      string            `json:"https" yaml:"https"`
	NoProxy    string            `json:"no_proxy" yaml:"no_proxy"`
	Registries map[string]string `json:"registries" yaml:"registries"`
}

// ProxyURLForImage returns the proxy URL to use when pulling img. The second
// result is false when this image should be pulled by the Docker daemon.
func (p DockerProxyConfiguration) ProxyURLForImage(img string) (string, bool) {
	named, err := reference.ParseNormalizedNamed(img)
	if err != nil {
		return "", false
	}

	domain := strings.ToLower(reference.Domain(named))
	if specific, ok := p.registryProxy(domain, reference.Path(named)); ok {
		if specific == "" || specific == "-" {
			return "", false
		}
		return specific, true
	}

	fallback := strings.TrimSpace(p.HTTPS)
	if fallback == "" {
		fallback = strings.TrimSpace(p.HTTP)
	}
	if fallback == "" || proxyExcluded(domain, p.NoProxy) {
		return "", false
	}
	return fallback, true
}

// MergeInstallEnv appends proxy variables the installer container should see.
// A variable already set by the egg is left alone.
func (p DockerProxyConfiguration) MergeInstallEnv(env []string) []string {
	httpProxy := strings.TrimSpace(p.HTTP)
	httpsProxy := strings.TrimSpace(p.HTTPS)
	if httpsProxy == "" {
		httpsProxy = httpProxy
	}
	if httpProxy == "" {
		httpProxy = httpsProxy
	}
	if httpProxy == "" && httpsProxy == "" {
		return env
	}

	want := map[string]string{
		"HTTP_PROXY":  httpProxy,
		"http_proxy":  httpProxy,
		"HTTPS_PROXY": httpsProxy,
		"https_proxy": httpsProxy,
	}
	if noProxy := strings.TrimSpace(p.NoProxy); noProxy != "" {
		want["NO_PROXY"] = noProxy
		want["no_proxy"] = noProxy
	}

	present := make(map[string]struct{}, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			present[key] = struct{}{}
		}
	}

	keys := make([]string, 0, len(want))
	for key := range want {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := present[key]; ok || want[key] == "" {
			continue
		}
		env = append(env, key+"="+want[key])
	}
	return env
}

func (p DockerProxyConfiguration) registryProxy(domain, imagePath string) (string, bool) {
	found := false
	best := ""
	bestScore := -1
	for registry, proxy := range p.Registries {
		regDomain, regPath, ok := parseDockerRegistryReference(registry)
		if !ok || regDomain != domain || !registryPathMatchesImage(regPath, imagePath) {
			continue
		}
		score := len(regDomain) + len(regPath)
		if score > bestScore {
			best = strings.TrimSpace(proxy)
			bestScore = score
			found = true
		}
	}
	return best, found
}

func proxyExcluded(host, noProxy string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || strings.TrimSpace(noProxy) == "" {
		return false
	}
	hostNoPort := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostNoPort = h
	}

	for _, item := range strings.Split(noProxy, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if item == "*" {
			return true
		}
		if strings.Contains(item, "/") {
			ip := net.ParseIP(hostNoPort)
			if ip == nil {
				continue
			}
			_, cidr, err := net.ParseCIDR(item)
			if err == nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		itemHost := item
		if h, _, err := net.SplitHostPort(item); err == nil {
			itemHost = h
		}
		itemHost = strings.TrimPrefix(itemHost, ".")
		if host == item || hostNoPort == itemHost || strings.HasSuffix(hostNoPort, "."+itemHost) {
			return true
		}
	}
	return false
}

// MaskProxy hides credentials embedded in a proxy URL before it is logged.
func MaskProxy(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	return u.Redacted()
}
