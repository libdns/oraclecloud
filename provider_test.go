package oraclecloud

import (
	"context"
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
