package relay

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	EnvEndpoints    = "SOYAOS_RELAY_ENDPOINTS"
	EnvFreeRateMbps = "SOYAOS_RELAY_FREE_RATE_MBPS"
)

// CloudConfig is the non-secret part of the hosted relay configuration.
// Routing tokens are minted per session and must never be stored here.
type CloudConfig struct {
	Endpoints    []string
	FreeRateMbps int
}

// LoadCloudConfigFromEnv parses a comma-separated region endpoint list. An
// empty list is valid for Solo/Cluster installations that do not use hosted
// fallback.
func LoadCloudConfigFromEnv() (CloudConfig, error) {
	return ParseCloudConfig(os.Getenv(EnvEndpoints), os.Getenv(EnvFreeRateMbps))
}

func ParseCloudConfig(rawEndpoints, rawRate string) (CloudConfig, error) {
	config := CloudConfig{FreeRateMbps: 10}
	for _, raw := range strings.Split(rawEndpoints, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "relay+udp" || u.Host == "" {
			return CloudConfig{}, fmt.Errorf("relay: %s contains an invalid relay+udp endpoint", EnvEndpoints)
		}
		if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return CloudConfig{}, fmt.Errorf("relay: static endpoints must not contain tokens, credentials, queries, or fragments")
		}
		config.Endpoints = append(config.Endpoints, u.String())
	}
	if strings.TrimSpace(rawRate) != "" {
		rate, err := strconv.Atoi(strings.TrimSpace(rawRate))
		if err != nil || rate < 1 || rate > 10000 {
			return CloudConfig{}, fmt.Errorf("relay: %s must be an integer from 1 to 10000", EnvFreeRateMbps)
		}
		config.FreeRateMbps = rate
	}
	return config, nil
}
