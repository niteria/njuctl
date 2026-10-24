{ lib, ... }:
{
  options = {
    router = lib.mkOption {
      type = lib.types.str;
      description = "Router HTTP(S) base URL.";
    };
    username = lib.mkOption {
      type = lib.types.str;
      default = "admin";
      description = "Router login name.";
    };
    passwordFile = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = "Runtime path to the admin password; never a Nix store secret.";
    };
    passwordCommand = lib.mkOption {
      type = lib.types.nullOr lib.types.lines;
      default = null;
      description = "Shell command that prints the admin password at runtime.";
    };
    reservations = lib.mkOption {
      description = "Complete desired static reservation list, keyed by descriptive name.";
      type = lib.types.attrsOf (
        lib.types.submodule {
          options = {
            mac = lib.mkOption {
              type = lib.types.str;
              description = "Device MAC address.";
            };
            ip = lib.mkOption {
              type = lib.types.str;
              description = "Reserved IPv4 address.";
            };
          };
        }
      );
    };
  };
}
