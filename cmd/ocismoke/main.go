package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	oraclecloud "github.com/Djelibeybi/libdns-oraclecloud"
	"github.com/Djelibeybi/libdns-oraclecloud/internal/txtrdata"
	"github.com/libdns/libdns"
)

func main() {
	log.SetFlags(0)
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var (
		zone           = flag.String("zone", "", "DNS zone name or OCI zone OCID (required)")
		name           = flag.String("name", "", "Relative TXT record name to create; defaults to a random _libdns-smoke-* label")
		value          = flag.String("value", "", "TXT value to write; defaults to a generated smoke-test value")
		ttl            = flag.Duration("ttl", 30*time.Second, "TXT record TTL")
		timeout        = flag.Duration("timeout", 2*time.Minute, "Overall timeout for the smoke test")
		auth           = flag.String("auth", "auto", "Provider auth mode: auto, config_file, environment, api_key")
		configFile     = flag.String("config-file", "", "OCI config file path")
		configProfile  = flag.String("config-profile", "", "OCI config profile name")
		privateKeyPath = flag.String("private-key-path", "", "Path to OCI private key PEM")
		tenancyOCID    = flag.String("tenancy-ocid", "", "OCI tenancy OCID")
		userOCID       = flag.String("user-ocid", "", "OCI user OCID")
		fingerprint    = flag.String("fingerprint", "", "OCI API key fingerprint")
		region         = flag.String("region", "", "OCI region")
		scope          = flag.String("scope", "", "OCI DNS scope: GLOBAL or PRIVATE")
		viewID         = flag.String("view-id", "", "OCI DNS view OCID; required for private zones by name")
	)
	flag.Parse()

	if strings.TrimSpace(*zone) == "" {
		return fmt.Errorf("missing required -zone")
	}

	suffix := randomSuffix()
	recordName := strings.TrimSpace(*name)
	if recordName == "" {
		recordName = "_libdns-smoke-" + suffix
	}

	txtValue := *value
	if txtValue == "" {
		txtValue = "libdns-oraclecloud smoke test " + suffix
	}

	privateKey := strings.TrimSpace(os.Getenv("OCI_CLI_KEY_CONTENT"))
	privateKeyPassphrase := strings.TrimSpace(os.Getenv("OCI_CLI_PASSPHRASE"))
	if strings.EqualFold(strings.TrimSpace(*auth), "api_key") && strings.TrimSpace(*privateKeyPath) == "" && privateKey == "" {
		return fmt.Errorf("api_key auth requires -private-key-path or OCI_CLI_KEY_CONTENT")
	}

	provider := &oraclecloud.Provider{
		Auth:                 strings.TrimSpace(*auth),
		ConfigFile:           strings.TrimSpace(*configFile),
		ConfigProfile:        strings.TrimSpace(*configProfile),
		PrivateKey:           privateKey,
		PrivateKeyPath:       strings.TrimSpace(*privateKeyPath),
		PrivateKeyPassphrase: privateKeyPassphrase,
		TenancyOCID:          strings.TrimSpace(*tenancyOCID),
		UserOCID:             strings.TrimSpace(*userOCID),
		Fingerprint:          strings.TrimSpace(*fingerprint),
		Region:               strings.TrimSpace(*region),
		Scope:                strings.TrimSpace(*scope),
		ViewID:               strings.TrimSpace(*viewID),
	}

	record := libdns.TXT{
		Name: recordName,
		TTL:  *ttl,
		Text: txtValue,
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	log.Printf("Zone: %s", *zone)
	log.Printf("Auth mode: %s", provider.Auth)
	log.Printf("Creating TXT record %q with TTL %s", record.Name, record.TTL)

	created, err := provider.AppendRecords(ctx, *zone, []libdns.Record{record})
	if err != nil {
		return fmt.Errorf("append TXT record: %w", err)
	}
	if len(created) == 0 {
		return fmt.Errorf("append TXT record: provider returned no created records")
	}
	cleanupNeeded := true
	defer func() {
		if !cleanupNeeded {
			return
		}

		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if _, err := provider.DeleteRecords(cleanupCtx, *zone, created); err != nil {
			log.Printf("Cleanup warning: delete TXT record %q: %v", record.Name, err)
		}
	}()

	printRecords("Created", created)

	log.Printf("Fetching records to confirm access and visibility")
	records, err := provider.GetRecords(ctx, *zone)
	if err != nil {
		return fmt.Errorf("get records after append: %w", err)
	}

	if !containsTXT(records, record.Name, txtValue) {
		return fmt.Errorf("TXT record %q with expected value was not found after creation", record.Name)
	}
	log.Printf("Confirmed TXT record is visible via GetRecords")

	log.Printf("Deleting TXT record %q", record.Name)
	deleted, err := provider.DeleteRecords(ctx, *zone, created)
	if err != nil {
		return fmt.Errorf("delete TXT record: %w", err)
	}
	if len(deleted) == 0 {
		return fmt.Errorf("delete TXT record: provider returned no deleted records")
	}

	printRecords("Deleted", deleted)

	records, err = provider.GetRecords(ctx, *zone)
	if err != nil {
		return fmt.Errorf("get records after delete: %w", err)
	}
	if containsTXT(records, record.Name, txtValue) {
		return fmt.Errorf("TXT record %q is still present after delete", record.Name)
	}
	cleanupNeeded = false

	log.Printf("Smoke test passed")
	return nil
}

func printRecords(label string, records []libdns.Record) {
	for _, record := range records {
		rr := record.RR()
		log.Printf("%s: %s %s %s %s", label, rr.Name, rr.Type, rr.TTL, rr.Data)
	}
}

func containsTXT(records []libdns.Record, name, value string) bool {
	for _, record := range records {
		txt, ok := record.(libdns.TXT)
		if !ok {
			continue
		}
		if txt.Name == name && txtMatchesValue(txt.Text, value) {
			return true
		}
	}
	return false
}

func txtMatchesValue(actual, expected string) bool {
	if actual == expected {
		return true
	}

	normalized, err := txtrdata.Parse(actual)
	return err == nil && normalized == expected
}

func randomSuffix() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}
