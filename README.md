# libdns Oracle Cloud

`libdns` provider for Oracle Cloud Infrastructure DNS.

This package implements:

- `GetRecords`
- `AppendRecords`
- `SetRecords`
- `DeleteRecords`
- `ListZones`

## Authentication

The provider currently keeps OCI auth intentionally simple:

- explicit API key fields on `oraclecloud.Provider`
- OCI config file credentials
- OCI environment variables

Supported `Auth` values:

- `""` or `auto`
- `api_key`
- `config_file`
- `environment`

`instance_principal` is also wired through, but the package is primarily aimed at API-key based usage for now.

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

The provider accepts the same practical OCI API-key inputs commonly used by other Go DNS clients:

- `OCI_PRIVATE_KEY` or `OCI_PRIVATE_KEY_PATH`
- `OCI_PRIVATE_KEY_PASSWORD`
- `OCI_TENANCY_OCID`
- `OCI_USER_OCID`
- `OCI_FINGERPRINT`
- `OCI_REGION`
- `OCI_CONFIG_FILE`
- `OCI_CONFIG_PROFILE`

You can override the `OCI` prefix with `EnvironmentPrefix`.

## Notes

- For private zones accessed by name, OCI requires `ViewID`.
- `SetRecords` is atomic per RRSet because OCI exposes RRSet replacement as a single operation, but it is not atomic across multiple distinct RRSets.
- The module path is `github.com/Djelibeybi/libdns-oraclecloud`.
