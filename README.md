# njuctl

Small, dependency-free Go CLI for Sagemcom Funbox routers. Uses their
authenticated web API, maintains cookies and session context, and logs out on
completion (including command failures).

## Build and run

```sh
go build -o njuctl .
./njuctl --router http://192.0.2.1 --password-file /path/to/admin-password leases
./njuctl --router http://192.0.2.1 --password-file /path/to/admin-password devices
./njuctl --router http://192.0.2.1 --password-file /path/to/admin-password reserve 02:00:00:00:00:22 192.0.2.22
./njuctl --router http://192.0.2.1 --password-file /path/to/admin-password --json leases
```

With Nix, `nix develop` provides Go; `nix build` produces `result/bin/njuctl`.
The flake exports both a named `njuctl` app and a default app:

```sh
nix run .#njuctl -- --router http://192.0.2.1 --password-file /path/to/admin-password leases
nix run . -- --help
```

Replace `.` with the repository's path or flake URL to run it from elsewhere.

```sh
nix run github:niteria/njuctl#njuctl -- --help
```

Flags go **before** the command. `njuctl --help` lists them.
`devices` always outputs the router's device inventory as JSON.

`reserve` lists current static leases before writing. An exact match is a
successful no-op. Conflicting MAC or IP reservations produce an error. A newly
added reservation is only reported as successful after matching readback.
The router validates the address against its DHCP configuration.

## Authentication

`--router` is required; there is no default address. Username defaults to the
vendor's `admin` login and can be changed with `--username`.
Password sources, in priority order:

1. `--password-file PATH`: actual admin password, trailing CR/LF removed.
2. `NJU_PASSWORD`: actual admin password.

Both sources take the **actual router admin password**. Credential discovery,
Wi-Fi key derivation, and site-specific addresses belong in a caller or wrapper.

No credentials are saved by the utility. Authentication follows the router's
web UI: a form POST to `/authenticate` with the parameters also in the query,
then JSON RPC calls to `/ws` with `X-Context` and session cookies.
Redirects are refused, and transport errors omit URLs to avoid exposing the
password. Use the HTTP or HTTPS scheme supported by your router.

Funbox session cookie names contain a slash (for example,
`abcd1234/sessid`), which Go's standard cookie jar rejects. The client retains
raw cookie pairs in memory for this single router origin to match the web UI.

## Wrapper interface

A wrapper can supply `--router` and `--username`, obtain the admin password at
runtime via `NJU_PASSWORD` or `--password-file`, then forward command arguments.
The utility has no dependency on NetworkManager or local machine configuration.

## Checks

```sh
go test -race ./...
go vet ./...
```

## License

MIT — see [LICENSE](LICENSE).
