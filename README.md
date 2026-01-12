# AWS Quota Dashboard

A simple, fast web dashboard to view and export AWS service quotas across all regions.

## Features

- **Multi-region Support** - Query quotas across all AWS regions concurrently
- **All Services** - Access quotas for all AWS services via Service Quotas API
- **Smart Caching** - 5-minute TTL cache to reduce API calls
- **Multiple Export Formats** - JSON and HTML report export
- **Clean Web UI** - Simple single-page interface with filtering and search

## Quick Start

### Prerequisites

- Go 1.21+
- AWS credentials configured (`~/.aws/credentials` or environment variables)
- IAM permissions (see [iam-policy.json](iam-policy.json))

### IAM Permissions

Minimum required permissions:
- `servicequotas:ListServices`
- `servicequotas:ListServiceQuotas`
- `servicequotas:GetServiceQuota`
- `ec2:DescribeRegions`

Or attach AWS managed policies: `ServiceQuotasReadOnlyAccess` + `AmazonEC2ReadOnlyAccess`

### Run Locally

```bash
# Clone the repository
git clone https://github.com/leoshi01/aws-quota-dashboard.git
cd aws-quota-dashboard

# Install dependencies
go mod download

# Run the server
make run
# or
go run ./cmd/server

# Open http://localhost:8080
```

### Using Docker

```bash
# Build the image
make docker-build

# Run with AWS credentials
docker run -p 8080:8080 \
  -e AWS_ACCESS_KEY_ID=your-key \
  -e AWS_SECRET_ACCESS_KEY=your-secret \
  aws-quota-dashboard:0.1.0
```

## API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/regions` | List all enabled AWS regions |
| GET | `/api/services` | List all available services |
| GET | `/api/quotas` | Get quotas (supports `region`, `service`, `search` params) |
| POST | `/api/refresh` | Clear cache and refresh data |
| GET | `/api/export/json` | Export quotas as JSON |
| GET | `/api/export/html` | Export quotas as HTML report |

### Query Parameters

```
GET /api/quotas?region=us-east-1&service=ec2&search=instance
```

- `region` - Filter by region (default: all regions)
- `service` - Filter by service code (e.g., `ec2`, `lambda`)
- `search` - Search in quota name, service name, or service code

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `PORT` | `8080` | Server port |
| `AWS_REGION` | `us-east-1` | Default AWS region |
| `AWS_ACCESS_KEY_ID` | - | AWS access key |
| `AWS_SECRET_ACCESS_KEY` | - | AWS secret key |

## Project Structure

```
aws-quota-dashboard/
├── cmd/server/main.go      # Entry point
├── internal/
│   ├── aws/                # AWS SDK wrappers
│   ├── cache/              # In-memory cache
│   ├── handler/            # HTTP handlers
│   └── model/              # Data models
├── web/templates/          # HTML templates
├── Dockerfile
├── Makefile
└── README.md
```

## License

MIT
