package config

import "testing"

func TestProxyURLForImage(t *testing.T) {
	cfg := DockerProxyConfiguration{
		HTTP:    "http://10.0.0.8:7890",
		HTTPS:   "http://10.0.0.8:7890",
		NoProxy: "localhost,127.0.0.1,docker.imxbt.cn",
		Registries: map[string]string{
			"ghcr.io":   "http://10.0.0.8:7890",
			"quay.io":   "http://10.0.0.9:7890",
			"docker.io": "-",
		},
	}

	tests := []struct {
		image string
		proxy string
		ok    bool
	}{
		{image: "ghcr.io/pterodactyl/yolks:java_17", proxy: "http://10.0.0.8:7890", ok: true},
		{image: "quay.io/parkervcp/yolks:debian", proxy: "http://10.0.0.9:7890", ok: true},
		{image: "python:3.11", ok: false},
		{image: "docker.imxbt.cn/library/eclipse-temurin:21", ok: false},
		{image: "registry.example.com/egg:latest", proxy: "http://10.0.0.8:7890", ok: true},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got, ok := cfg.ProxyURLForImage(tt.image)
			if ok != tt.ok || got != tt.proxy {
				t.Fatalf("ProxyURLForImage(%q) = %q, %v; want %q, %v", tt.image, got, ok, tt.proxy, tt.ok)
			}
		})
	}
}

func TestMergeInstallEnvKeepsEggProxy(t *testing.T) {
	cfg := DockerProxyConfiguration{
		HTTP:    "http://10.0.0.8:7890",
		NoProxy: "localhost",
	}
	got := cfg.MergeInstallEnv([]string{"STARTUP=x", "HTTP_PROXY=http://egg:1"})
	for _, entry := range got {
		if entry == "HTTP_PROXY=http://10.0.0.8:7890" {
			t.Fatalf("egg HTTP_PROXY was replaced: %v", got)
		}
	}
	if !containsEnv(got, "http_proxy=http://10.0.0.8:7890") || !containsEnv(got, "HTTPS_PROXY=http://10.0.0.8:7890") {
		t.Fatalf("missing proxy variables: %v", got)
	}
	if !containsEnv(got, "NO_PROXY=localhost") {
		t.Fatalf("missing no_proxy: %v", got)
	}
}

func TestMergeInstallEnvDisabled(t *testing.T) {
	got := (DockerProxyConfiguration{}).MergeInstallEnv([]string{"A=b"})
	if len(got) != 1 || got[0] != "A=b" {
		t.Fatalf("env changed with no proxy: %v", got)
	}
}

func containsEnv(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}
