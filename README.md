# rudeproxy
An intentionally flaky passthrough proxy for testing network resiliency.

You point your client at rudeproxy instead of your real API. It forwards each request to the upstream and is sometimes rude about it.

Currently the only rude thing it does is drop the connection *after* the upstream has already responded. Your client sees a dead connection but the upstream handled the request. The intent is to 

This is an early MVP. If you need fault injection today, see [toxiproxy](https://github.com/Shopify/toxiproxy). More kinds of rudeness (and vendor-specific plugins) are planned.

## Building

You need Go 1.27.2 or newer.

```sh
make build        # puts the binary in bin/rudeproxy
```

Or just run it with `go run ./cmd/rudeproxy serve ...`.

## Usage

```sh
rudeproxy serve -upstream https://api.example.com -response-drop-rate 0.1
```

Then send requests to `http://127.0.0.1:8080` as if it were the upstream. About 1 in 10 of them will get their connection dropped.

Flags for `serve`:

| Flag | Default | What it does |
| --- | --- | --- |
| `-upstream` | (required) | Where to forward requests. Has to be a bare origin like `https://api.example.com`, with no path. |
| `-listen` | `127.0.0.1:8080` | Address to listen on. |
| `-response-drop-rate` | `0` | Chance (0 to 1) of dropping the connection after the upstream responds. At 0 it's a plain passthrough. |
| `-seed` | `0` | Random seed for faults. 0 picks one at random. Set it if you want the same sequence of drops each run. |
| `-upstream-timeout` | `80s` | How long to wait for the upstream. |
| `-log-level` | `info` | `debug`, `info`, `warn`, or `error`. |

`rudeproxy version` prints the version.

A few things to know:

- It's a reverse proxy, not a forward proxy. `CONNECT` and absolute-URL requests are refused.
- Request bodies are capped at 1 MiB and response bodies at 16 MiB for now.
- Credentials in the upstream URL are rejected so they don't end up in logs.

## Development

```sh
make test   # go test -race ./...
make lint   # golangci-lint
make all    # fmt, vet, lint, test, build
```

## License

MIT
