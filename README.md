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

## Declarative configuration with Nix

The flake exports `lib.mkRouter`, which evaluates typed Nix modules and produces
a JSON configuration, a configured CLI package, and an app. For example, add
`inputs.njuctl.url = "github:niteria/njuctl"` to your own flake and define:

```nix
let
  router = inputs.njuctl.lib.mkRouter {
    inherit pkgs;
    modules = [ ./router.nix ];
  };
in {
  packages.${system}.njuctl = router.package;
  apps.${system}.njuctl = router.app;
}
```

`router.nix` is a Nix module, with `pkgs` available as a module argument:

```nix
{
  router = "http://192.0.2.1";
  username = "admin";
  passwordFile = "/run/secrets/router-admin-password";
  reservations = {
    printer = {
      mac = "02:00:00:00:00:22";
      ip = "192.0.2.22";
    };
  };
}
```

Alternatively, `passwordCommand` can obtain the admin password at invocation
time. Its stdout becomes `NJU_PASSWORD`; an already-set `NJU_PASSWORD` takes
precedence over the command. `passwordCommand` and `passwordFile` are mutually
exclusive. Keep secrets out of Nix expressions and the Nix store.

```sh
# In your configuration repository:
nix run .#njuctl -- sync           # preview the diff
nix run .#njuctl -- --apply sync   # reconcile and verify
nix run .#njuctl -- leases
```

The configured reservation list is authoritative: sync removes static
reservations absent from it, replaces changed addresses, and adds missing
entries. Deletes precede adds to handle IP swaps. Dynamic leases and other router
settings are not managed. Descriptive names label the config and plan; they do
not rename devices on the router.

Preview performs no writes. Apply verifies the entire static lease list after
the changes. Writes are not atomic: on failure the CLI stops and reports the
failed change; rerun a preview to inspect the remaining diff. A matching config
causes no writes. Avoid concurrent router edits during a sync.

`router.json` exposes the generated configuration as a Nix derivation, and
`modules.router` exports the option module for other integrations.

### JSON interface

The Go CLI also accepts generated JSON directly:

```json
{"reservations":[{"name":"printer","mac":"02:00:00:00:00:22","ip":"192.0.2.22"}]}
```

```sh
njuctl --router http://192.0.2.1 --password-file /path/to/password --config router.json sync
njuctl --router http://192.0.2.1 --password-file /path/to/password --config router.json --apply sync
```

Unknown fields, duplicate addresses/MACs, and invalid addresses are rejected
before contacting the router. `{"reservations":[]}` explicitly declares an
empty list; a missing or null `reservations` field is rejected.

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
For declarative sync it also supplies `--config` (as `lib.mkRouter` does).
The utility has no dependency on NetworkManager or local machine configuration.

## Checks

```sh
go test -race ./...
go vet ./...
```

## License

MIT — see [LICENSE](LICENSE).
