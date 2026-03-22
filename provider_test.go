package oraclecloud

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/libdns/libdns"
	"github.com/oracle/oci-go-sdk/v65/common"
	ocidns "github.com/oracle/oci-go-sdk/v65/dns"
)

func TestGetRecordsConvertsOCIRecords(t *testing.T) {
	provider := &Provider{
		client: &fakeDNSClient{
			zones: map[string]string{
				"example.com": "example.com",
			},
			records: map[string][]ocidns.Record{
				"example.com": {
					testOCIRecord("example.com", "A", "192.0.2.10", 300),
					testOCIRecord("www.example.com", "CNAME", "example.com.", 600),
				},
			},
		},
	}

	records, err := provider.GetRecords(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("GetRecords() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("GetRecords() len = %d, want 2", len(records))
	}

	root, ok := records[0].(libdns.Address)
	if !ok {
		t.Fatalf("records[0] type = %T, want libdns.Address", records[0])
	}
	if root.Name != "@" || root.IP.String() != "192.0.2.10" || root.TTL != 300*time.Second {
		t.Fatalf("unexpected root record: %+v", root)
	}

	cname, ok := records[1].(libdns.CNAME)
	if !ok {
		t.Fatalf("records[1] type = %T, want libdns.CNAME", records[1])
	}
	if cname.Name != "www" || cname.Target != "example.com." {
		t.Fatalf("unexpected cname record: %+v", cname)
	}
}

func TestAppendRecordsAddsOnlyNewRecords(t *testing.T) {
	client := &fakeDNSClient{
		zones: map[string]string{
			"example.com": "example.com",
		},
		records: map[string][]ocidns.Record{
			"example.com": {
				testOCIRecord("example.com", "A", "192.0.2.1", 300),
			},
		},
	}

	provider := &Provider{client: client}
	input := []libdns.Record{
		libdns.Address{Name: "@", TTL: 300 * time.Second, IP: mustAddr(t, "192.0.2.1")},
		libdns.Address{Name: "@", TTL: 300 * time.Second, IP: mustAddr(t, "192.0.2.2")},
	}

	added, err := provider.AppendRecords(context.Background(), "example.com", input)
	if err != nil {
		t.Fatalf("AppendRecords() error = %v", err)
	}
	if len(added) != 1 {
		t.Fatalf("AppendRecords() len = %d, want 1", len(added))
	}

	record := added[0].(libdns.Address)
	if record.IP.String() != "192.0.2.2" {
		t.Fatalf("added record IP = %s, want 192.0.2.2", record.IP)
	}
	if got := len(client.records["example.com"]); got != 2 {
		t.Fatalf("client record count = %d, want 2", got)
	}
}

func TestSetRecordsReplacesRRSet(t *testing.T) {
	client := &fakeDNSClient{
		zones: map[string]string{
			"example.com": "example.com",
		},
		records: map[string][]ocidns.Record{
			"example.com": {
				testOCIRecord("example.com", "A", "192.0.2.1", 300),
				testOCIRecord("example.com", "A", "192.0.2.2", 300),
				testOCIRecord("example.com", "TXT", "\"keep me\"", 300),
			},
		},
	}

	provider := &Provider{client: client}
	updated, err := provider.SetRecords(context.Background(), "example.com", []libdns.Record{
		libdns.Address{Name: "@", TTL: 120 * time.Second, IP: mustAddr(t, "198.51.100.10")},
	})
	if err != nil {
		t.Fatalf("SetRecords() error = %v", err)
	}
	if len(updated) != 1 {
		t.Fatalf("SetRecords() len = %d, want 1", len(updated))
	}

	if got := len(client.records["example.com"]); got != 2 {
		t.Fatalf("client record count = %d, want 2", got)
	}
}

func TestDeleteRecordsSupportsWildcardDeleteSemantics(t *testing.T) {
	client := &fakeDNSClient{
		zones: map[string]string{
			"example.com": "example.com",
		},
		records: map[string][]ocidns.Record{
			"example.com": {
				testOCIRecord("example.com", "TXT", "\"one\"", 300),
				testOCIRecord("example.com", "TXT", "\"two\"", 600),
				testOCIRecord("example.com", "A", "192.0.2.1", 300),
			},
		},
	}

	provider := &Provider{client: client}
	deleted, err := provider.DeleteRecords(context.Background(), "example.com", []libdns.Record{
		libdns.RR{Name: "@", Type: "TXT"},
	})
	if err != nil {
		t.Fatalf("DeleteRecords() error = %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("DeleteRecords() len = %d, want 2", len(deleted))
	}
	if got := len(client.records["example.com"]); got != 1 {
		t.Fatalf("client record count = %d, want 1", got)
	}
}

func TestListZones(t *testing.T) {
	provider := &Provider{
		CompartmentID: "ocid1.compartment.oc1..example",
		client: &fakeDNSClient{
			listedZones: []ocidns.ZoneSummary{
				{Name: common.String("example.com")},
				{Name: common.String("example.net.")},
			},
		},
	}

	zones, err := provider.ListZones(context.Background())
	if err != nil {
		t.Fatalf("ListZones() error = %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("ListZones() len = %d, want 2", len(zones))
	}
	if zones[0].Name != "example.com." || zones[1].Name != "example.net." {
		t.Fatalf("unexpected zones: %+v", zones)
	}
}

func TestRecordToDetailsQuotesTXTData(t *testing.T) {
	details, err := recordToDetails(libdns.TXT{
		Name: "@",
		TTL:  30 * time.Second,
		Text: "libdns oracle cloud",
	}, "example.com.")
	if err != nil {
		t.Fatalf("recordToDetails() error = %v", err)
	}
	if got := value(details.Rdata); got != `"libdns oracle cloud"` {
		t.Fatalf("recordToDetails() Rdata = %q, want %q", got, `"libdns oracle cloud"`)
	}
}

func TestRecordToDetailsQuotesRawTXTRData(t *testing.T) {
	details, err := recordToDetails(libdns.RR{
		Name: "@",
		TTL:  30 * time.Second,
		Type: "TXT",
		Data: "libdns oracle cloud",
	}, "example.com.")
	if err != nil {
		t.Fatalf("recordToDetails() error = %v", err)
	}
	if got := value(details.Rdata); got != `"libdns oracle cloud"` {
		t.Fatalf("recordToDetails() Rdata = %q, want %q", got, `"libdns oracle cloud"`)
	}
}

func TestToLibdnsRecordParsesTXTRData(t *testing.T) {
	record, err := toLibdnsRecord(testOCIRecord("smoke.example.com", "TXT", `"libdns-oraclecloud" " smoke"`, 30), "example.com.")
	if err != nil {
		t.Fatalf("toLibdnsRecord() error = %v", err)
	}

	txt, ok := record.(libdns.TXT)
	if !ok {
		t.Fatalf("record type = %T, want libdns.TXT", record)
	}
	if txt.Name != "smoke" {
		t.Fatalf("TXT name = %q, want %q", txt.Name, "smoke")
	}
	if txt.Text != "libdns-oraclecloud smoke" {
		t.Fatalf("TXT text = %q, want %q", txt.Text, "libdns-oraclecloud smoke")
	}
}

func TestEnvironmentConfigurationProviderUsesOracleCLIVariables(t *testing.T) {
	t.Setenv(envCLITenancy, "ocid1.tenancy.oc1..example")
	t.Setenv(envCLIUser, "ocid1.user.oc1..example")
	t.Setenv(envCLIFingerprint, "12:34:56:78")
	t.Setenv(envCLIRegion, "us-phoenix-1")

	keyFile, err := os.CreateTemp(t.TempDir(), "oci-key-*.pem")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	if _, err := keyFile.WriteString(testPrivateKeyPEM); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := keyFile.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	t.Setenv(envCLIKeyFile, keyFile.Name())

	provider := &Provider{Auth: "environment"}
	configProvider, err := provider.environmentConfigurationProvider()
	if err != nil {
		t.Fatalf("environmentConfigurationProvider() error = %v", err)
	}

	if got, err := configProvider.TenancyOCID(); err != nil || got != "ocid1.tenancy.oc1..example" {
		t.Fatalf("TenancyOCID() = %q, %v", got, err)
	}
	if got, err := configProvider.UserOCID(); err != nil || got != "ocid1.user.oc1..example" {
		t.Fatalf("UserOCID() = %q, %v", got, err)
	}
	if got, err := configProvider.KeyFingerprint(); err != nil || got != "12:34:56:78" {
		t.Fatalf("KeyFingerprint() = %q, %v", got, err)
	}
	if got, err := configProvider.Region(); err != nil || got != "us-phoenix-1" {
		t.Fatalf("Region() = %q, %v", got, err)
	}
}

type fakeDNSClient struct {
	zones       map[string]string
	records     map[string][]ocidns.Record
	listedZones []ocidns.ZoneSummary
}

func (f *fakeDNSClient) GetZone(_ context.Context, req ocidns.GetZoneRequest) (ocidns.GetZoneResponse, error) {
	nameOrID := *req.ZoneNameOrId
	zoneName, ok := f.zones[nameOrID]
	if !ok {
		zoneName = nameOrID
	}
	return ocidns.GetZoneResponse{
		Zone: ocidns.Zone{Name: common.String(zoneName)},
	}, nil
}

func (f *fakeDNSClient) GetZoneRecords(_ context.Context, req ocidns.GetZoneRecordsRequest) (ocidns.GetZoneRecordsResponse, error) {
	return ocidns.GetZoneRecordsResponse{
		RecordCollection: ocidns.RecordCollection{
			Items: cloneOCIRecords(f.records[*req.ZoneNameOrId]),
		},
	}, nil
}

func (f *fakeDNSClient) PatchZoneRecords(_ context.Context, req ocidns.PatchZoneRecordsRequest) (ocidns.PatchZoneRecordsResponse, error) {
	zone := *req.ZoneNameOrId
	records := cloneOCIRecords(f.records[zone])

	for _, op := range req.Items {
		switch op.Operation {
		case ocidns.RecordOperationOperationAdd:
			candidate := ocidns.Record{
				Domain: op.Domain,
				Rdata:  op.Rdata,
				Rtype:  op.Rtype,
				Ttl:    op.Ttl,
			}
			if !containsOCIRecord(records, candidate) {
				records = append(records, candidate)
			}
		case ocidns.RecordOperationOperationRemove:
			var filtered []ocidns.Record
			for _, record := range records {
				if !matchesOCIRecordOperation(record, op) {
					filtered = append(filtered, record)
				}
			}
			records = filtered
		}
	}

	f.records[zone] = records
	return ocidns.PatchZoneRecordsResponse{
		RecordCollection: ocidns.RecordCollection{Items: cloneOCIRecords(records)},
	}, nil
}

func (f *fakeDNSClient) UpdateRRSet(_ context.Context, req ocidns.UpdateRRSetRequest) (ocidns.UpdateRRSetResponse, error) {
	zone := *req.ZoneNameOrId
	var filtered []ocidns.Record
	for _, record := range f.records[zone] {
		if sameString(record.Domain, req.Domain) && sameString(record.Rtype, req.Rtype) {
			continue
		}
		filtered = append(filtered, record)
	}

	for _, item := range req.Items {
		filtered = append(filtered, ocidns.Record{
			Domain: item.Domain,
			Rdata:  item.Rdata,
			Rtype:  item.Rtype,
			Ttl:    item.Ttl,
		})
	}

	f.records[zone] = filtered
	return ocidns.UpdateRRSetResponse{
		RecordCollection: ocidns.RecordCollection{Items: cloneOCIRecords(filtered[len(filtered)-len(req.Items):])},
	}, nil
}

func (f *fakeDNSClient) ListZones(_ context.Context, _ ocidns.ListZonesRequest) (ocidns.ListZonesResponse, error) {
	return ocidns.ListZonesResponse{Items: f.listedZones}, nil
}

func cloneOCIRecords(records []ocidns.Record) []ocidns.Record {
	cloned := make([]ocidns.Record, 0, len(records))
	for _, record := range records {
		cloned = append(cloned, testOCIRecord(value(record.Domain), value(record.Rtype), value(record.Rdata), intValue(record.Ttl)))
	}
	return cloned
}

func containsOCIRecord(records []ocidns.Record, target ocidns.Record) bool {
	for _, record := range records {
		if sameString(record.Domain, target.Domain) &&
			sameString(record.Rtype, target.Rtype) &&
			sameString(record.Rdata, target.Rdata) &&
			intValue(record.Ttl) == intValue(target.Ttl) {
			return true
		}
	}
	return false
}

func matchesOCIRecordOperation(record ocidns.Record, op ocidns.RecordOperation) bool {
	if !sameString(record.Domain, op.Domain) {
		return false
	}
	if op.Rtype != nil && !sameString(record.Rtype, op.Rtype) {
		return false
	}
	if op.Rdata != nil && !sameString(record.Rdata, op.Rdata) {
		return false
	}
	if op.Ttl != nil && intValue(record.Ttl) != intValue(op.Ttl) {
		return false
	}
	return true
}

func testOCIRecord(domain, rtype, rdata string, ttl int) ocidns.Record {
	return ocidns.Record{
		Domain: common.String(domain),
		Rtype:  common.String(rtype),
		Rdata:  common.String(rdata),
		Ttl:    common.Int(ttl),
	}
}

func sameString(left, right *string) bool {
	return value(left) == value(right)
}

func value(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func intValue(ptr *int) int {
	if ptr == nil {
		return 0
	}
	return *ptr
}

const testPrivateKeyPEM = `-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEAx4j+5W8i2J6pcv6sQIZ4YxQfS6LzO0nmyK+dZ4k2nVqZKo84
NLc5m9X9T5vWrl+ZlO/4AI1QkXl2WD1lkr8xSQ8K+WTixMz8CcpB3M17j0t1mHpy
6s7E4t4FQaArl6Vt00ns7PZ7enYEgMq1lMcq89BI2v5k4VmbKFp5xYVbP0cO1gCt
G6m+N2Qm3Y+bmNV6fpQFk5tFM0/s+QiPmpLqH4FuR5RqGZYxQywM66ieoBqM1w7c
fn4iW2lTj5jS8kT9AwYFZW1W+ubz95V7RA5iW0m4N61efV0QebjEXF+WmKzCawhp
otObKHa+7ixN5gQv8v2mukYjoX9iIwN90pce/QIDAQABAoIBADm9qXcnM3uSsh2+
6JfA2eN0RaK0LAzqk6T8t2a1gTnT3P1He4pNnNwA22pkcQp7Dpqj7z2JiC+o9oGx
ly6svYJOVkHR1x9hPw6qL7YFNoTK8hYboY9/zsJ+uNoeKxmI/r5k0d7lM9YtOWqI
F2p2mDUMH6sd30h2C2fQBmL88v3iZ4zV6aJSMEf34Gq9l7J2pVmpL6BE5Rh4L6q8
P0tUEQmCjg5YjFMi2dM8WeXQUBWmTSlF3Dc38C1bwlJ6BYm0KCw8lAQn0O69o8SL
P6yx38aDg0Q3sYIG4Vx2BpW3SgX4O4ayk7Wq8Ww9V3fYQw1Q4a2UBtm7z3ZlrjlG
dz3fMGECgYEA8Mrl0eMOG0Y1a7z7m2rx4XlsZqW4a7HkT3On8mK2yDI+IAXm9y8n
0bz2fE+JEE+ahbAdnD6mX3TY8J9OFrjFrw0kOj7fM6Y3w8Bh7JOii0v9cewT0WZz
L4AZ2A4sAgRu46A34l1hIe8w/1oCAlPckjNzM0NdpxgqA8isYy1hlM0CgYEA0piQ
9a7pFT8K2+8e1TJjGvXz5+ltF6Dn1vvGrf7v+AB9TFn/fx8KoRPr1A7v4fmjTEmP
kM8Mz0h5jDgFQ9v7IP0r3Pv2i6k1xF7c4s4X8Sdrh3fe85Hsn9PTVY3S4mNXM7h4
kkK+7iBpt1vu3iBDvVYQ4HMLr3pt0e0ct4BGv/ECgYEApTxj0Ik3YIvd2I1B5j7e
7nzNhE8P+6H/zNMLU3A4L5otXGbkZt8QAPJxhWg5t8Ce3HGVBNk1FE5Vn9I9Y8bc
0t4DClxCHuRNDM6Q6A1l1ZfF2ckvKSK7w0u3GJskZ/KSBw4GmJr6FJ6ruQ3jTch1
uZ64v7dE/2I5qk0ryVQW9sUCgYA7KaKKqlKhGqjP6feD3M35/OMH1g8LyP1FUAv7
zsH/0nF0QzT4QiWzk2I+4Z4T8JqYOdY6X1qFnDFg99EdpK2gWsArwSeVj9lF1pWl
sn9K0+RzR9n6ylTn5J4Xl2tJrJw6hWgPrsjMnYVTTFp1hSSUDPG5xP9dBiO2STMd
VJ3PwQKBgBRb4Yh3iR2oOrk9c1q4c6kX3xKhTQq3CRJqjSxKs8SL4e4wHuM4OWKr
O3tzzbjYV7eJ1u2Z6BZ7dBn1M4l9MZl8AfM5Rm5s8shGwSyvZbIjO+Pk4ecf/8gk
dnIHT7xjMFLhNu/cF9MqT7SCcaGX+XlTpDkOSsLyeN+StT4eoHh3
-----END RSA PRIVATE KEY-----
`
