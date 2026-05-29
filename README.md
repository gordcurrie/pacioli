# Pacioli

Adjusted Cost Base (ACB) tracker for Canadian investors. Supports Questrade accounts (margin, cash, TFSA, RRSP, RESP, LRSP, SRSP), multi-currency securities, and CRA-compliant capital gains reporting.

Named after [Luca Pacioli](https://en.wikipedia.org/wiki/Luca_Pacioli), the father of double-entry bookkeeping.

## Features

- ACB tracking per security across all non-registered accounts (average cost method)
- CAD and native-currency cost tracking with exchange rates
- Commission and FX conversion fee inclusion in ACB
- Norbert's Gambit transaction pairing
- Return of Capital adjustments
- Superficial loss flagging
- Questrade API sync + manual/CSV entry

## Setup

### Prerequisites

- Go 1.26+
- `golangci-lint` — `brew install golangci-lint`
- `gosec` — `go install github.com/securego/gosec/v2/cmd/gosec@latest`
- `govulncheck` — `go install golang.org/x/vuln/cmd/govulncheck@latest`
- `air` — `go install github.com/air-verse/air@latest` (hot reload for local dev)

### Local development

```bash
git clone https://github.com/gordcurrie/pacioli
cd pacioli
go mod download
make install-hooks   # installs pre-commit lint/sec/vuln checks
make dev             # start with hot reload (requires air)
```

Server starts at `http://localhost:8080`.

### Docker

```bash
docker compose up --build
```

Data persists in the `pacioli-data` Docker volume.

## Development

```bash
make check    # run all checks (lint + gosec + govulncheck)
make test     # run tests with race detector
make build    # compile binary to bin/pacioli
```

See [CLAUDE.md](CLAUDE.md) for architecture and conventions.

## License

MIT
