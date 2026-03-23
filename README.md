# libdns Oracle Cloud

`libdns` provider for Oracle Cloud Infrastructure DNS.

This module currently requires Go 1.25 or newer.

This package implements:

- `GetRecords`
- `AppendRecords`
- `SetRecords`
- `DeleteRecords`
- `ListZones`

## Authentication

OCI SDKs support several authentication methods. This provider supports the two
primary methods: API keys, for authentication outside OCI, and
`instance_principal`, for authentication from instances within OCI.

All OCI SDKs support some or all of Oracle's standard environment variables for
authentication configuration, so this provider does too.

The recommended and most prominently documented configuration style is the
standard OCI config file, usually `~/.oci/config`, because it matches Oracle's
preferred user workflow and works cleanly with existing OCI tooling.

See the OCI Developer Guide for more on the [Authentication Methods](https://docs.oracle.com/en-us/iaas/Content/API/Concepts/sdk_authentication_methods.htm) and [Environment Variables](https://docs.oracle.com/en-us/iaas/Content/API/SDKDocs/clienvironmentvariables.htm).

Supported `Auth` values:

- `""` or `auto`
- `api_key`
- `config_file`
- `environment`
- `instance_principal`

The `api_key`, `config_file`, and `environment` values are all API-key authentication;
they only differ in where the credentials come from.

`resource_principal` and token-based authentication are not currently supported.

If you set `Auth` to `auto` or `config_file` with a valid `~/.oci/config` file available, or set `Auth` to `instance_principal` on an OCI instance with the appropriate policies applied, no further configuration is required to manage public DNS zones. However, both of these methods require `ViewID` to be configured to manage private zones by name.

For `environment`, or when populating the provider fields directly, the minimum required values are `TenancyOCID`, `UserOCID`, `Fingerprint`, `Region`, and either `PrivateKeyPath` or inline `PrivateKey`. `PrivateKeyPassphrase` is only needed when the private key is passphrase-protected.

`CompartmentID` is required to List Zones.

## Provider Fields

```go
provider := oraclecloud.Provider{
    Auth:               "auto",
    ConfigFile:         "~/.oci/config",
    ConfigProfile:      "DEFAULT",
    TenancyOCID:        "ocid1.tenancy.oc1..example",
    UserOCID:           "ocid1.user.oc1..example",
    Fingerprint:        "12:34:56:78:90:ab:cd:ef:12:34:56:78:90:ab:cd:ef",
    PrivateKeyPath:     "~/.oci/oci_api_key.pem",
    PrivateKeyPassphrase: "",
    Region:             "us-phoenix-1",
    Scope:              "GLOBAL",
    ViewID:             "",
    CompartmentID:      "ocid1.compartment.oc1..example",
}
```

## Environment Variables

The provider accepts Oracle's documented OCI CLI environment variables:

- `OCI_CLI_KEY_CONTENT` or `OCI_CLI_KEY_FILE`
- `OCI_CLI_PASSPHRASE`
- `OCI_CLI_TENANCY`
- `OCI_CLI_USER`
- `OCI_CLI_FINGERPRINT`
- `OCI_CLI_REGION`
- `OCI_CLI_CONFIG_FILE`
- `OCI_CLI_PROFILE`

## Smoke Test

You can verify real OCI auth and TXT-record write access locally with the bundled smoke-test command:

```sh
go run ./cmd/ocismoke \
  -auth config_file \
  -config-file ~/.oci/config \
  -config-profile DEFAULT \
  -zone example.com
```

By default it creates a random `_libdns-smoke-*` TXT record, confirms it can read it back, and then deletes it.

Useful flags:

- `-auth auto|config_file|environment|api_key`
- `-zone` zone name or OCI zone OCID
- `-view-id` for private zones accessed by name
- `-scope GLOBAL|PRIVATE`
- `-name` to override the generated label
- `-value` to override the generated TXT value

For safety, the smoke-test command does not accept inline secret flags such as
`--private-key` or `--private-key-passphrase`. Use `-config-file`, `-private-key-path`,
or Oracle's `OCI_CLI_*` environment variables instead.

## Versioning

This module follows Semantic Versioning, with Git tags like `v1.2.3`.

For Go modules, that means:

- `fix:` conventional commits map to patch releases
- `feat:` conventional commits map to minor releases
- `feat!:` or any commit with a `BREAKING CHANGE:` footer maps to a major release
- if this module ever releases `v2+`, the Go module path must also change to include the major suffix
  such as `github.com/libdns/oraclecloud/v2`

GitHub Actions uses `release-please` to turn conventional commits merged to `main` into release PRs,
SemVer tags, changelog updates, and GitHub Releases.

## Notes

- In `Auth: "auto"` mode, explicit API-key fields take precedence over config-file auth. If you want to
  force use of `~/.oci/config`, set `Auth: "config_file"`.
- For private zones accessed by name, OCI requires `ViewID`.
- `SetRecords` is atomic per RRSet because OCI exposes RRSet replacement as a single operation, but it is not atomic across multiple distinct RRSets.
- The module path is `github.com/libdns/oraclecloud`.
