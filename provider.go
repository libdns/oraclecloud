// Package oraclecloud implements a libdns provider for Oracle Cloud
// Infrastructure DNS.
package oraclecloud

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Djelibeybi/libdns-oraclecloud/internal/txtrdata"
	"github.com/libdns/libdns"
	"github.com/oracle/oci-go-sdk/v65/common"
	ociauth "github.com/oracle/oci-go-sdk/v65/common/auth"
	ocidns "github.com/oracle/oci-go-sdk/v65/dns"
)

const (
	envCLIProfile     = "OCI_CLI_PROFILE"
	envCLIUser        = "OCI_CLI_USER"
	envCLIRegion      = "OCI_CLI_REGION"
	envCLIFingerprint = "OCI_CLI_FINGERPRINT"
	envCLIKeyFile     = "OCI_CLI_KEY_FILE"
	envCLIKeyContent  = "OCI_CLI_KEY_CONTENT"
	envCLITenancy     = "OCI_CLI_TENANCY"
	envCLIPassphrase  = "OCI_CLI_PASSPHRASE"
	envCLIConfigFile  = "OCI_CLI_CONFIG_FILE"
)

// Provider facilitates DNS record manipulation with Oracle Cloud Infrastructure.
//
// Authentication is intentionally kept simple:
//   - explicit API key fields on the provider
//   - OCI config file/profile
//   - OCI_* environment variables
//
// The provider is safe for concurrent use.
type Provider struct {
	Auth string `json:"auth,omitempty"`

	ConfigFile    string `json:"config_file,omitempty"`
	ConfigProfile string `json:"config_profile,omitempty"`

	PrivateKey           string `json:"private_key,omitempty"`
	PrivateKeyPath       string `json:"private_key_path,omitempty"`
	PrivateKeyPassphrase string `json:"private_key_passphrase,omitempty"`
	TenancyOCID          string `json:"tenancy_ocid,omitempty"`
	UserOCID             string `json:"user_ocid,omitempty"`
	Fingerprint          string `json:"fingerprint,omitempty"`
	Region               string `json:"region,omitempty"`

	Scope         string `json:"scope,omitempty"`
	ViewID        string `json:"view_id,omitempty"`
	CompartmentID string `json:"compartment_id,omitempty"`

	mu        sync.Mutex `json:"-"`
	client    dnsAPI     `json:"-"`
	clientErr error      `json:"-"`
}

// GetRecords lists all the records in the zone.
func (p *Provider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	client, err := p.getClient()
	if err != nil {
		return nil, err
	}

	ref, err := p.resolveZone(ctx, client, zone)
	if err != nil {
		return nil, err
	}

	records, err := p.getZoneRecords(ctx, client, ref.reference)
	if err != nil {
		return nil, err
	}

	return p.toLibdnsRecords(records, ref.name)
}

// AppendRecords adds records to the zone. It returns the records that were added.
func (p *Provider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	if len(records) == 0 {
		return nil, nil
	}

	client, err := p.getClient()
	if err != nil {
		return nil, err
	}

	ref, err := p.resolveZone(ctx, client, zone)
	if err != nil {
		return nil, err
	}

	before, err := p.getZoneRecords(ctx, client, ref.reference)
	if err != nil {
		return nil, err
	}

	ops := make([]ocidns.RecordOperation, 0, len(records))
	for _, record := range records {
		op, err := recordToOperation(record, ref.name, ocidns.RecordOperationOperationAdd)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	req := ocidns.PatchZoneRecordsRequest{
		ZoneNameOrId: common.String(ref.reference),
		PatchZoneRecordsDetails: ocidns.PatchZoneRecordsDetails{
			Items: ops,
		},
	}
	if err := p.applyPatchOptions(&req); err != nil {
		return nil, err
	}

	if _, err := client.PatchZoneRecords(ctx, req); err != nil {
		return nil, err
	}

	after, err := p.getZoneRecords(ctx, client, ref.reference)
	if err != nil {
		return nil, err
	}

	return diffAddedRecords(before, after, records, ref.name)
}

// SetRecords sets the records in the zone, either by updating existing records or creating new ones.
// It returns the updated records.
func (p *Provider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	if len(records) == 0 {
		return nil, nil
	}

	client, err := p.getClient()
	if err != nil {
		return nil, err
	}

	ref, err := p.resolveZone(ctx, client, zone)
	if err != nil {
		return nil, err
	}

	grouped, err := groupRecordsByRRSet(records)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var updated []libdns.Record
	for _, key := range keys {
		group := grouped[key]
		items := make([]ocidns.RecordDetails, 0, len(group))
		for _, record := range group {
			item, err := recordToDetails(record, ref.name)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}

		req := ocidns.UpdateRRSetRequest{
			ZoneNameOrId: common.String(ref.reference),
			Domain:       items[0].Domain,
			Rtype:        items[0].Rtype,
			UpdateRrSetDetails: ocidns.UpdateRrSetDetails{
				Items: items,
			},
		}
		if err := p.applyUpdateOptions(&req); err != nil {
			return nil, err
		}

		resp, err := client.UpdateRRSet(ctx, req)
		if err != nil {
			return nil, err
		}

		converted, err := p.toLibdnsRecords(resp.Items, ref.name)
		if err != nil {
			return nil, err
		}
		updated = append(updated, converted...)
	}

	return updated, nil
}

// DeleteRecords deletes the specified records from the zone. It returns the records that were deleted.
func (p *Provider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	if len(records) == 0 {
		return nil, nil
	}

	client, err := p.getClient()
	if err != nil {
		return nil, err
	}

	ref, err := p.resolveZone(ctx, client, zone)
	if err != nil {
		return nil, err
	}

	before, err := p.getZoneRecords(ctx, client, ref.reference)
	if err != nil {
		return nil, err
	}

	deleted, err := findDeletedRecords(before, records, ref.name)
	if err != nil {
		return nil, err
	}
	if len(deleted) == 0 {
		return nil, nil
	}

	ops := make([]ocidns.RecordOperation, 0, len(records))
	for _, record := range records {
		op, err := deleteCriterionToOperation(record, ref.name)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}

	req := ocidns.PatchZoneRecordsRequest{
		ZoneNameOrId: common.String(ref.reference),
		PatchZoneRecordsDetails: ocidns.PatchZoneRecordsDetails{
			Items: ops,
		},
	}
	if err := p.applyPatchOptions(&req); err != nil {
		return nil, err
	}

	if _, err := client.PatchZoneRecords(ctx, req); err != nil {
		return nil, err
	}

	return deleted, nil
}

// ListZones lists the zones available in the configured compartment.
func (p *Provider) ListZones(ctx context.Context) ([]libdns.Zone, error) {
	if strings.TrimSpace(p.CompartmentID) == "" {
		return nil, fmt.Errorf("compartment_id is required to list zones")
	}

	client, err := p.getClient()
	if err != nil {
		return nil, err
	}

	var zones []libdns.Zone
	var page *string

	for {
		req := ocidns.ListZonesRequest{
			CompartmentId: common.String(p.CompartmentID),
			Limit:         common.Int64(100),
			Page:          page,
		}
		if err := p.applyListOptions(&req); err != nil {
			return nil, err
		}

		resp, err := client.ListZones(ctx, req)
		if err != nil {
			return nil, err
		}

		for _, zone := range resp.Items {
			if zone.Name == nil || *zone.Name == "" {
				continue
			}
			zones = append(zones, libdns.Zone{Name: normalizeZoneName(*zone.Name)})
		}

		if resp.OpcNextPage == nil || *resp.OpcNextPage == "" {
			break
		}
		page = resp.OpcNextPage
	}

	return zones, nil
}

func (p *Provider) getClient() (dnsAPI, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.client != nil || p.clientErr != nil {
		return p.client, p.clientErr
	}

	configProvider, err := p.configurationProvider()
	if err != nil {
		p.clientErr = err
		return nil, err
	}

	client, err := ocidns.NewDnsClientWithConfigurationProvider(configProvider)
	if err != nil {
		p.clientErr = err
		return nil, err
	}
	if region := strings.TrimSpace(p.Region); region != "" {
		client.SetRegion(region)
	}

	p.client = sdkDNSClient{client: client}
	return p.client, nil
}

func (p *Provider) configurationProvider() (common.ConfigurationProvider, error) {
	authMode := strings.ToLower(strings.TrimSpace(p.Auth))
	if authMode == "" {
		authMode = "auto"
	}

	switch authMode {
	case "auto":
		if p.hasInlineOrFileCredentials() {
			return p.rawConfigurationProvider()
		}
		if p.hasConfigFileHints() || fileExists(defaultConfigFilePath()) {
			return p.fileConfigurationProvider()
		}
		if p.hasEnvironmentCredentials() {
			return p.environmentConfigurationProvider()
		}
		return nil, fmt.Errorf("no OCI authentication configuration found; set provider fields, OCI_* environment variables, or an OCI config file")
	case "api_key", "user_principal":
		if p.hasInlineOrFileCredentials() {
			return p.rawConfigurationProvider()
		}
		if p.hasEnvironmentCredentials() {
			return p.environmentConfigurationProvider()
		}
		return p.fileConfigurationProvider()
	case "config_file":
		return p.fileConfigurationProvider()
	case "environment":
		return p.environmentConfigurationProvider()
	case "instance_principal":
		return ociauth.InstancePrincipalConfigurationProvider()
	default:
		return nil, fmt.Errorf("unsupported auth mode %q", p.Auth)
	}
}

func (p *Provider) rawConfigurationProvider() (common.ConfigurationProvider, error) {
	privateKey, err := p.privateKeyPEM()
	if err != nil {
		return nil, err
	}

	tenancy := strings.TrimSpace(p.TenancyOCID)
	user := strings.TrimSpace(p.UserOCID)
	fingerprint := strings.TrimSpace(p.Fingerprint)
	region := strings.TrimSpace(p.Region)

	if tenancy == "" || user == "" || fingerprint == "" || region == "" || privateKey == "" {
		return nil, fmt.Errorf("tenancy_ocid, user_ocid, fingerprint, region, and private_key/private_key_path are required for API key authentication")
	}

	var passphrase *string
	if value := strings.TrimSpace(p.PrivateKeyPassphrase); value != "" {
		passphrase = common.String(value)
	}

	return common.NewRawConfigurationProvider(tenancy, user, region, fingerprint, privateKey, passphrase), nil
}

func (p *Provider) fileConfigurationProvider() (common.ConfigurationProvider, error) {
	path := strings.TrimSpace(p.ConfigFile)
	if path == "" {
		path = defaultConfigFilePath()
	}
	path = expandHome(path)

	profile := strings.TrimSpace(p.ConfigProfile)
	if profile == "" {
		profile = envValue(envCLIProfile)
	}
	if profile == "" {
		profile = "DEFAULT"
	}

	if !fileExists(path) {
		return nil, fmt.Errorf("OCI config file not found at %q", path)
	}

	return common.ConfigurationProviderFromFileWithProfile(path, profile, strings.TrimSpace(p.PrivateKeyPassphrase))
}

func (p *Provider) environmentConfigurationProvider() (common.ConfigurationProvider, error) {
	privateKey := strings.TrimSpace(p.PrivateKey)
	if privateKey == "" {
		privateKey = envValue(envCLIKeyContent)
	}
	if privateKey == "" {
		privateKeyPath := envValue(envCLIKeyFile)
		if privateKeyPath == "" {
			return nil, fmt.Errorf("%s or %s is required for environment authentication", envCLIKeyContent, envCLIKeyFile)
		}
		keyBytes, err := os.ReadFile(expandHome(privateKeyPath))
		if err != nil {
			return nil, fmt.Errorf("reading OCI private key from %q: %w", privateKeyPath, err)
		}
		privateKey = string(keyBytes)
	}

	passphrase := strings.TrimSpace(p.PrivateKeyPassphrase)
	if passphrase == "" {
		passphrase = envValue(envCLIPassphrase)
	}

	tenancy := firstNonEmpty(strings.TrimSpace(p.TenancyOCID), envValue(envCLITenancy))
	user := firstNonEmpty(strings.TrimSpace(p.UserOCID), envValue(envCLIUser))
	fingerprint := firstNonEmpty(strings.TrimSpace(p.Fingerprint), envValue(envCLIFingerprint))
	region := firstNonEmpty(strings.TrimSpace(p.Region), envValue(envCLIRegion))

	if tenancy == "" || user == "" || fingerprint == "" || region == "" {
		return nil, fmt.Errorf("%s, %s, %s, and %s are required for environment authentication", envCLITenancy, envCLIUser, envCLIFingerprint, envCLIRegion)
	}

	var passphrasePtr *string
	if passphrase != "" {
		passphrasePtr = common.String(passphrase)
	}

	return common.NewRawConfigurationProvider(tenancy, user, region, fingerprint, privateKey, passphrasePtr), nil
}

func (p *Provider) privateKeyPEM() (string, error) {
	if key := strings.TrimSpace(p.PrivateKey); key != "" {
		return key, nil
	}
	if path := strings.TrimSpace(p.PrivateKeyPath); path != "" {
		keyBytes, err := os.ReadFile(expandHome(path))
		if err != nil {
			return "", fmt.Errorf("reading private key from %q: %w", path, err)
		}
		return string(keyBytes), nil
	}
	return "", nil
}

func (p *Provider) hasInlineOrFileCredentials() bool {
	return strings.TrimSpace(p.TenancyOCID) != "" ||
		strings.TrimSpace(p.UserOCID) != "" ||
		strings.TrimSpace(p.Fingerprint) != "" ||
		strings.TrimSpace(p.Region) != "" ||
		strings.TrimSpace(p.PrivateKey) != "" ||
		strings.TrimSpace(p.PrivateKeyPath) != ""
}

func (p *Provider) hasConfigFileHints() bool {
	return strings.TrimSpace(p.ConfigFile) != "" ||
		strings.TrimSpace(p.ConfigProfile) != ""
}

func (p *Provider) hasEnvironmentCredentials() bool {
	return envValue(envCLITenancy) != "" ||
		envValue(envCLIUser) != "" ||
		envValue(envCLIFingerprint) != "" ||
		envValue(envCLIRegion) != "" ||
		envValue(envCLIKeyFile) != "" ||
		envValue(envCLIKeyContent) != ""
}

func (p *Provider) resolveZone(ctx context.Context, client dnsAPI, zone string) (zoneRef, error) {
	if strings.TrimSpace(zone) == "" {
		return zoneRef{}, fmt.Errorf("zone is required")
	}

	if !isOCID(zone) {
		normalized := normalizeZoneName(zone)
		return zoneRef{
			name:      normalized,
			reference: strings.TrimSuffix(normalized, "."),
		}, nil
	}

	req := ocidns.GetZoneRequest{
		ZoneNameOrId: common.String(zone),
	}
	if err := p.applyGetZoneOptions(&req); err != nil {
		return zoneRef{}, err
	}

	resp, err := client.GetZone(ctx, req)
	if err != nil {
		return zoneRef{}, err
	}
	if resp.Name == nil || *resp.Name == "" {
		return zoneRef{}, fmt.Errorf("OCI zone %q did not include a zone name in the response", zone)
	}

	return zoneRef{
		name:      normalizeZoneName(*resp.Name),
		reference: zone,
	}, nil
}

func (p *Provider) getZoneRecords(ctx context.Context, client dnsAPI, zone string) ([]ocidns.Record, error) {
	var records []ocidns.Record
	var page *string

	for {
		req := ocidns.GetZoneRecordsRequest{
			ZoneNameOrId: common.String(zone),
			Limit:        common.Int64(100),
			Page:         page,
		}
		if err := p.applyGetRecordsOptions(&req); err != nil {
			return nil, err
		}

		resp, err := client.GetZoneRecords(ctx, req)
		if err != nil {
			return nil, err
		}
		records = append(records, resp.Items...)

		if resp.OpcNextPage == nil || *resp.OpcNextPage == "" {
			break
		}
		page = resp.OpcNextPage
	}

	return records, nil
}

func (p *Provider) toLibdnsRecords(records []ocidns.Record, zone string) ([]libdns.Record, error) {
	converted := make([]libdns.Record, 0, len(records))
	for _, record := range records {
		item, err := toLibdnsRecord(record, zone)
		if err != nil {
			return nil, err
		}
		converted = append(converted, item)
	}
	return converted, nil
}

func (p *Provider) applyGetZoneOptions(req *ocidns.GetZoneRequest) error {
	scope, err := p.getZoneScope()
	if err != nil {
		return err
	}
	req.Scope = scope
	if viewID := strings.TrimSpace(p.ViewID); viewID != "" {
		req.ViewId = common.String(viewID)
	}
	return nil
}

func (p *Provider) applyGetRecordsOptions(req *ocidns.GetZoneRecordsRequest) error {
	scope, err := p.getZoneRecordsScope()
	if err != nil {
		return err
	}
	req.Scope = scope
	if viewID := strings.TrimSpace(p.ViewID); viewID != "" {
		req.ViewId = common.String(viewID)
	}
	return nil
}

func (p *Provider) applyPatchOptions(req *ocidns.PatchZoneRecordsRequest) error {
	scope, err := p.patchZoneRecordsScope()
	if err != nil {
		return err
	}
	req.Scope = scope
	if viewID := strings.TrimSpace(p.ViewID); viewID != "" {
		req.ViewId = common.String(viewID)
	}
	return nil
}

func (p *Provider) applyUpdateOptions(req *ocidns.UpdateRRSetRequest) error {
	scope, err := p.updateRRSetScope()
	if err != nil {
		return err
	}
	req.Scope = scope
	if viewID := strings.TrimSpace(p.ViewID); viewID != "" {
		req.ViewId = common.String(viewID)
	}
	return nil
}

func (p *Provider) applyListOptions(req *ocidns.ListZonesRequest) error {
	scope, err := p.listZonesScope()
	if err != nil {
		return err
	}
	req.Scope = scope
	if viewID := strings.TrimSpace(p.ViewID); viewID != "" {
		req.ViewId = common.String(viewID)
	}
	return nil
}

func (p *Provider) scopeValue() (string, error) {
	scope := strings.ToUpper(strings.TrimSpace(p.Scope))
	switch scope {
	case "", "GLOBAL", "PRIVATE":
		return scope, nil
	default:
		return "", fmt.Errorf("unsupported scope %q; expected GLOBAL or PRIVATE", p.Scope)
	}
}

func (p *Provider) getZoneScope() (ocidns.GetZoneScopeEnum, error) {
	scope, err := p.scopeValue()
	if err != nil {
		return "", err
	}
	switch scope {
	case "":
		return "", nil
	case "GLOBAL":
		return ocidns.GetZoneScopeGlobal, nil
	default:
		return ocidns.GetZoneScopePrivate, nil
	}
}

func (p *Provider) getZoneRecordsScope() (ocidns.GetZoneRecordsScopeEnum, error) {
	scope, err := p.scopeValue()
	if err != nil {
		return "", err
	}
	switch scope {
	case "":
		return "", nil
	case "GLOBAL":
		return ocidns.GetZoneRecordsScopeGlobal, nil
	default:
		return ocidns.GetZoneRecordsScopePrivate, nil
	}
}

func (p *Provider) patchZoneRecordsScope() (ocidns.PatchZoneRecordsScopeEnum, error) {
	scope, err := p.scopeValue()
	if err != nil {
		return "", err
	}
	switch scope {
	case "":
		return "", nil
	case "GLOBAL":
		return ocidns.PatchZoneRecordsScopeGlobal, nil
	default:
		return ocidns.PatchZoneRecordsScopePrivate, nil
	}
}

func (p *Provider) updateRRSetScope() (ocidns.UpdateRRSetScopeEnum, error) {
	scope, err := p.scopeValue()
	if err != nil {
		return "", err
	}
	switch scope {
	case "":
		return "", nil
	case "GLOBAL":
		return ocidns.UpdateRRSetScopeGlobal, nil
	default:
		return ocidns.UpdateRRSetScopePrivate, nil
	}
}

func (p *Provider) listZonesScope() (ocidns.ListZonesScopeEnum, error) {
	scope, err := p.scopeValue()
	if err != nil {
		return "", err
	}
	switch scope {
	case "":
		return "", nil
	case "GLOBAL":
		return ocidns.ListZonesScopeGlobal, nil
	default:
		return ocidns.ListZonesScopePrivate, nil
	}
}

func recordToDetails(record libdns.Record, zone string) (ocidns.RecordDetails, error) {
	rr := record.RR()
	if strings.TrimSpace(rr.Name) == "" {
		return ocidns.RecordDetails{}, fmt.Errorf("record name is required")
	}
	if strings.TrimSpace(rr.Type) == "" {
		return ocidns.RecordDetails{}, fmt.Errorf("record type is required for %q", rr.Name)
	}
	if strings.TrimSpace(rr.Data) == "" {
		return ocidns.RecordDetails{}, fmt.Errorf("record data is required for %q %s", rr.Name, rr.Type)
	}

	rdata, err := recordRData(record)
	if err != nil {
		return ocidns.RecordDetails{}, err
	}

	return ocidns.RecordDetails{
		Domain: common.String(absoluteDomainForAPI(rr.Name, zone)),
		Rdata:  common.String(rdata),
		Rtype:  common.String(strings.ToUpper(rr.Type)),
		Ttl:    common.Int(ttlSeconds(rr.TTL)),
	}, nil
}

func recordToOperation(record libdns.Record, zone string, operation ocidns.RecordOperationOperationEnum) (ocidns.RecordOperation, error) {
	details, err := recordToDetails(record, zone)
	if err != nil {
		return ocidns.RecordOperation{}, err
	}

	return ocidns.RecordOperation{
		Domain:    details.Domain,
		Rdata:     details.Rdata,
		Rtype:     details.Rtype,
		Ttl:       details.Ttl,
		Operation: operation,
	}, nil
}

func deleteCriterionToOperation(record libdns.Record, zone string) (ocidns.RecordOperation, error) {
	rr := record.RR()
	if strings.TrimSpace(rr.Name) == "" {
		return ocidns.RecordOperation{}, fmt.Errorf("record name is required for delete operations")
	}

	op := ocidns.RecordOperation{
		Domain:    common.String(absoluteDomainForAPI(rr.Name, zone)),
		Operation: ocidns.RecordOperationOperationRemove,
	}
	if rr.Type != "" {
		op.Rtype = common.String(strings.ToUpper(rr.Type))
	}
	if rr.Data != "" {
		rdata, err := recordRData(record)
		if err != nil {
			return ocidns.RecordOperation{}, err
		}
		op.Rdata = common.String(rdata)
	}
	if rr.TTL != 0 {
		op.Ttl = common.Int(ttlSeconds(rr.TTL))
	}

	return op, nil
}

func toLibdnsRecord(record ocidns.Record, zone string) (libdns.Record, error) {
	if record.Domain == nil || record.Rtype == nil || record.Ttl == nil {
		return nil, fmt.Errorf("OCI record is missing one of domain, rtype, or ttl")
	}

	recordType := strings.ToUpper(*record.Rtype)
	if recordType == "TXT" {
		text, err := txtrdata.Parse(valueOrEmpty(record.Rdata))
		if err != nil {
			return nil, err
		}
		txt := libdns.TXT{
			Name: libdns.RelativeName(*record.Domain, zone),
			TTL:  time.Duration(*record.Ttl) * time.Second,
			Text: text,
		}
		txt.ProviderData = providerDataFromOCIRecord(record)
		return txt, nil
	}

	rr := libdns.RR{
		Name: libdns.RelativeName(*record.Domain, zone),
		TTL:  time.Duration(*record.Ttl) * time.Second,
		Type: recordType,
	}
	if record.Rdata != nil {
		rr.Data = *record.Rdata
	}

	parsed, _ := rr.Parse()
	providerData := providerDataFromOCIRecord(record)

	switch value := parsed.(type) {
	case libdns.Address:
		value.ProviderData = providerData
		return value, nil
	case libdns.CAA:
		value.ProviderData = providerData
		return value, nil
	case libdns.CNAME:
		value.ProviderData = providerData
		return value, nil
	case libdns.MX:
		value.ProviderData = providerData
		return value, nil
	case libdns.NS:
		value.ProviderData = providerData
		return value, nil
	case libdns.SRV:
		value.ProviderData = providerData
		return value, nil
	case libdns.ServiceBinding:
		value.ProviderData = providerData
		return value, nil
	case libdns.TXT:
		value.ProviderData = providerData
		return value, nil
	default:
		return parsed, nil
	}
}

func groupRecordsByRRSet(records []libdns.Record) (map[string][]libdns.Record, error) {
	grouped := make(map[string][]libdns.Record, len(records))
	for _, record := range records {
		rr := record.RR()
		if strings.TrimSpace(rr.Name) == "" {
			return nil, fmt.Errorf("record name is required")
		}
		if strings.TrimSpace(rr.Type) == "" {
			return nil, fmt.Errorf("record type is required for %q", rr.Name)
		}

		key := rrSetKey(rr)
		grouped[key] = append(grouped[key], record)
	}
	return grouped, nil
}

func diffAddedRecords(before, after []ocidns.Record, requested []libdns.Record, zone string) ([]libdns.Record, error) {
	requestedSets := make(map[string]struct{}, len(requested))
	for _, record := range requested {
		requestedSets[rrSetKey(record.RR())] = struct{}{}
	}

	beforeCounts := make(map[string]int)
	for _, record := range before {
		converted, err := toLibdnsRecord(record, zone)
		if err != nil {
			return nil, err
		}
		rr := converted.RR()
		if _, ok := requestedSets[rrSetKey(rr)]; !ok {
			continue
		}
		beforeCounts[canonicalRRKey(rr)]++
	}

	var added []libdns.Record
	for _, record := range after {
		converted, err := toLibdnsRecord(record, zone)
		if err != nil {
			return nil, err
		}
		rr := converted.RR()
		if _, ok := requestedSets[rrSetKey(rr)]; !ok {
			continue
		}

		key := canonicalRRKey(rr)
		if beforeCounts[key] > 0 {
			beforeCounts[key]--
			continue
		}
		added = append(added, converted)
	}

	return added, nil
}

func findDeletedRecords(existing []ocidns.Record, criteria []libdns.Record, zone string) ([]libdns.Record, error) {
	var deleted []libdns.Record
	for _, record := range existing {
		converted, err := toLibdnsRecord(record, zone)
		if err != nil {
			return nil, err
		}

		for _, criterion := range criteria {
			if matchesDeleteCriterion(converted.RR(), criterion.RR()) {
				deleted = append(deleted, converted)
				break
			}
		}
	}
	return deleted, nil
}

func matchesDeleteCriterion(existing, criterion libdns.RR) bool {
	if strings.TrimSpace(criterion.Name) == "" {
		return false
	}
	if !strings.EqualFold(existing.Name, criterion.Name) {
		return false
	}
	if criterion.Type != "" && !strings.EqualFold(existing.Type, criterion.Type) {
		return false
	}
	if criterion.TTL != 0 && ttlSeconds(existing.TTL) != ttlSeconds(criterion.TTL) {
		return false
	}
	if criterion.Data != "" && existing.Data != criterion.Data {
		return false
	}
	return true
}

func rrSetKey(rr libdns.RR) string {
	return strings.ToLower(rr.Name) + "\x00" + strings.ToUpper(rr.Type)
}

func canonicalRRKey(rr libdns.RR) string {
	return strings.ToLower(rr.Name) + "\x00" +
		strconvInt(ttlSeconds(rr.TTL)) + "\x00" +
		strings.ToUpper(rr.Type) + "\x00" +
		rr.Data
}

func ttlSeconds(ttl time.Duration) int {
	return int(ttl / time.Second)
}

func absoluteDomainForAPI(name, zone string) string {
	return strings.TrimSuffix(libdns.AbsoluteName(name, zone), ".")
}

func normalizeZoneName(zone string) string {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return ""
	}
	if strings.HasSuffix(zone, ".") {
		return zone
	}
	return zone + "."
}

func defaultConfigFilePath() string {
	if value := strings.TrimSpace(os.Getenv(envCLIConfigFile)); value != "" {
		return expandHome(value)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + "/.oci/config"
}

func expandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return home + path[1:]
	}
	return path
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func envValue(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func strconvInt(value int) string {
	return fmt.Sprintf("%d", value)
}

func isOCID(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "ocid1.")
}

func recordRData(record libdns.Record) (string, error) {
	rr := record.RR()
	if strings.TrimSpace(rr.Data) == "" {
		return "", fmt.Errorf("record data is required for %q %s", rr.Name, rr.Type)
	}

	if strings.EqualFold(rr.Type, "TXT") {
		text := rr.Data
		if normalized, err := txtrdata.Parse(rr.Data); err == nil {
			text = normalized
		}
		return strconv.Quote(text), nil
	}

	return rr.Data, nil
}

func providerDataFromOCIRecord(record ocidns.Record) providerRecordData {
	providerData := providerRecordData{}
	if record.RecordHash != nil {
		providerData.RecordHash = *record.RecordHash
	}
	if record.RrsetVersion != nil {
		providerData.RRSetVersion = *record.RrsetVersion
	}
	if record.Domain != nil {
		providerData.Domain = *record.Domain
	}
	if record.IsProtected != nil {
		providerData.IsProtected = *record.IsProtected
	}
	return providerData
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type zoneRef struct {
	name      string
	reference string
}

type providerRecordData struct {
	RecordHash   string `json:"record_hash,omitempty"`
	RRSetVersion string `json:"rrset_version,omitempty"`
	Domain       string `json:"domain,omitempty"`
	IsProtected  bool   `json:"is_protected,omitempty"`
}

type dnsAPI interface {
	GetZone(context.Context, ocidns.GetZoneRequest) (ocidns.GetZoneResponse, error)
	GetZoneRecords(context.Context, ocidns.GetZoneRecordsRequest) (ocidns.GetZoneRecordsResponse, error)
	PatchZoneRecords(context.Context, ocidns.PatchZoneRecordsRequest) (ocidns.PatchZoneRecordsResponse, error)
	UpdateRRSet(context.Context, ocidns.UpdateRRSetRequest) (ocidns.UpdateRRSetResponse, error)
	ListZones(context.Context, ocidns.ListZonesRequest) (ocidns.ListZonesResponse, error)
}

type sdkDNSClient struct {
	client ocidns.DnsClient
}

func (c sdkDNSClient) GetZone(ctx context.Context, req ocidns.GetZoneRequest) (ocidns.GetZoneResponse, error) {
	return c.client.GetZone(ctx, req)
}

func (c sdkDNSClient) GetZoneRecords(ctx context.Context, req ocidns.GetZoneRecordsRequest) (ocidns.GetZoneRecordsResponse, error) {
	return c.client.GetZoneRecords(ctx, req)
}

func (c sdkDNSClient) PatchZoneRecords(ctx context.Context, req ocidns.PatchZoneRecordsRequest) (ocidns.PatchZoneRecordsResponse, error) {
	return c.client.PatchZoneRecords(ctx, req)
}

func (c sdkDNSClient) UpdateRRSet(ctx context.Context, req ocidns.UpdateRRSetRequest) (ocidns.UpdateRRSetResponse, error) {
	return c.client.UpdateRRSet(ctx, req)
}

func (c sdkDNSClient) ListZones(ctx context.Context, req ocidns.ListZonesRequest) (ocidns.ListZonesResponse, error) {
	return c.client.ListZones(ctx, req)
}

// Interface guards
var (
	_ libdns.RecordGetter   = (*Provider)(nil)
	_ libdns.RecordAppender = (*Provider)(nil)
	_ libdns.RecordSetter   = (*Provider)(nil)
	_ libdns.RecordDeleter  = (*Provider)(nil)
	_ libdns.ZoneLister     = (*Provider)(nil)
)
