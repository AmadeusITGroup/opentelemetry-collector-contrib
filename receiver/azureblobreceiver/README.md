# Azure Blob Receiver

<!-- status autogenerated section -->
| Status        |           |
| ------------- |-----------|
| Stability     | [alpha]: logs, traces   |
| Distributions | [contrib] |
| Issues        | [![Open issues](https://img.shields.io/github/issues-search/open-telemetry/opentelemetry-collector-contrib?query=is%3Aissue%20is%3Aopen%20label%3Areceiver%2Fazureblob%20&label=open&color=orange&logo=opentelemetry)](https://github.com/open-telemetry/opentelemetry-collector-contrib/issues?q=is%3Aopen+is%3Aissue+label%3Areceiver%2Fazureblob) [![Closed issues](https://img.shields.io/github/issues-search/open-telemetry/opentelemetry-collector-contrib?query=is%3Aissue%20is%3Aclosed%20label%3Areceiver%2Fazureblob%20&label=closed&color=blue&logo=opentelemetry)](https://github.com/open-telemetry/opentelemetry-collector-contrib/issues?q=is%3Aclosed+is%3Aissue+label%3Areceiver%2Fazureblob) |
| Code coverage | [![codecov](https://codecov.io/github/open-telemetry/opentelemetry-collector-contrib/graph/main/badge.svg?component=receiver_azureblob)](https://app.codecov.io/gh/open-telemetry/opentelemetry-collector-contrib/tree/main/?components%5B0%5D=receiver_azureblob&displayType=list) |
| [Code Owners](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/CONTRIBUTING.md#becoming-a-code-owner)    | [@eedorenko](https://www.github.com/eedorenko), [@mx-psi](https://www.github.com/mx-psi) |

[alpha]: https://github.com/open-telemetry/opentelemetry-collector/blob/main/docs/component-stability.md#alpha
[contrib]: https://github.com/open-telemetry/opentelemetry-collector-releases/tree/main/distributions/otelcol-contrib
<!-- end autogenerated section -->


This receiver reads logs and trace data from [Azure Blob Storage](https://azure.microsoft.com/en-us/products/storage/blobs/).

## Configuration

The following settings are required:

- `event_hub:`
  `  endpoint:` (no default): Azure Event Hub endpoint triggering on the `Blob Create` event 

The following settings can be optionally configured:

- `auth` (default = connection_string): Specifies the used authentication method. Supported values are `connection_string`, `service_principal`, `default`.
- `cloud` (default = "AzureCloud"): Defines which Azure Cloud to use when using the `service_principal` authentication method. Value is either `AzureCloud` or `AzureUSGovernment`.
- `logs:`
  `  container_name:` (default = "logs"): Name of the blob container with the logs
- `traces:`
  `  container_name:` (default = "traces"): Name of the blob container with the traces

Authenticating using a connection string requires configuration of the following additional setting:

- `connection_string:` Azure Blob Storage connection key, which can be found in the Azure Blob Storage resource on the Azure Portal.

Authenticating using service principal requires configuration of the following additional settings:

- `service_principal:`
  `  tenant_id`
  `  client_id`
  `  client_secret`
- `storage_account_url:` Azure Storage Account url

The service principal method also requires the [Storage Blob Data Contributor](https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/storage#storage-blob-data-contributor) role on the logs and traces containers.

### Example configurations

Using connection string for authentication:

```yaml
receivers:
  azureblob:
    connection_string: DefaultEndpointsProtocol=https;AccountName=accountName;AccountKey=+idLkHYcL0MUWIKYHm2j4Q==;EndpointSuffix=core.windows.net
    event_hub:
      endpoint: Endpoint=sb://oteldata.servicebus.windows.net/;SharedAccessKeyName=otelhubbpollicy;SharedAccessKey=mPJVubIK5dJ6mLfZo1ucsdkLysLSQ6N7kddvsIcmoEs=;EntityPath=otellhub
```

Using service principal for authentication:

```yaml
receivers:
  azureblob:
    auth: service_principal
    service_principal:
      tenant_id: "${tenant_id}"
      client_id: "${client_id}"
      client_secret: "${env:CLIENT_SECRET}"
    storage_account_url: https://accountName.blob.core.windows.net
    event_hub:
      endpoint: Endpoint=sb://oteldata.servicebus.windows.net/;SharedAccessKeyName=otelhubbpollicy;SharedAccessKey=mPJVubIK5dJ6mLfZo1ucsdkLysLSQ6N7kddvsIcmoEs=;EntityPath=otellhub
```

The receiver subscribes [on the events](https://docs.microsoft.com/en-us/azure/storage/blobs/storage-blob-event-overview) published by Azure Blob Storage and handled by Azure Event Hub. When it receives `Blob Create` event, it reads the logs or traces from a corresponding blob and deletes it after processing.

