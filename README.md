# AWS Quota Dashboard

A simple, fast web dashboard to view and export AWS service quotas across all regions.

## Features

- **Multi-region Support** - Query quotas across all AWS regions concurrently
- **All Services** - Access quotas for all AWS services via Service Quotas API
- **Usage Metrics** - View current usage, limits, and usage percentage for quotas
- **Smart Defaults** - Configure default region and service for faster loading
- **Smart Caching** - Configurable TTL cache to reduce API calls
- **Multiple Export Formats** - JSON and HTML report export
- **Clean Web UI** - Simple single-page interface with filtering and search
- **Visual Warnings** - Color-coded usage percentages (red ≥90%, orange ≥75%, yellow ≥50%)

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

# (Optional) Configure defaults - create config.yaml
# See Configuration section below

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
| GET | `/api/config` | Get current configuration (default region, service) |
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

### Configuration File

Create a `config.yaml` file in the project root to customize defaults:

```yaml
# Default region to use when loading the dashboard
default_region: us-east-1

# Default service to filter (speeds up initial load)
default_service: ec2

# Server configuration
server:
  port: 8080
  
# Cache configuration (in minutes)
cache:
  ttl_minutes: 5

# Concurrency for fetching quotas from multiple regions
max_concurrency: 10
```

**Benefits of using config.yaml:**
- 🚀 **Faster Loading** - Start with a specific region and service (e.g., us-east-1 + ec2) instead of loading all regions
- ⚡ **Reduced API Calls** - Fewer requests to AWS Service Quotas API
- 🎯 **Better UX** - Dashboard loads with meaningful data immediately
- 💰 **Lower Costs** - Reduced API usage

### Environment Variables

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `PORT` | `8080` | Server port (overrides config.yaml) |
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
│   ├── config/             # Configuration management
│   ├── handler/            # HTTP handlers
│   └── model/              # Data models
├── web/templates/          # HTML templates
├── config.yaml             # Configuration file (optional)
├── Dockerfile
├── Makefile
└── README.md
```

## License

MIT
