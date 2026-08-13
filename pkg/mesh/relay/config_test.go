package relay

import "testing"

func TestParseCloudConfig(t *testing.T) {
	config, err := ParseCloudConfig(
		"relay+udp://relay-us-west.soyaos.ai:7443, relay+udp://relay-eu.soyaos.ai:7443",
		"12",
	)
	if err != nil {
		t.Fatalf("ParseCloudConfig: %v", err)
	}
	if len(config.Endpoints) != 2 || config.FreeRateMbps != 12 {
		t.Fatalf("config=%+v", config)
	}
}

func TestParseCloudConfig_RejectsPersistedToken(t *testing.T) {
	_, err := ParseCloudConfig("relay+udp://relay.example:7443?token=secret", "")
	if err == nil {
		t.Fatal("expected static token to be rejected")
	}
}
