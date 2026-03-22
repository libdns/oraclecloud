package oraclecloud

import (
	"net/netip"
	"testing"
)

func mustAddr(t *testing.T, value string) netip.Addr {
	t.Helper()

	addr, err := netip.ParseAddr(value)
	if err != nil {
		t.Fatalf("ParseAddr(%q): %v", value, err)
	}
	return addr
}
