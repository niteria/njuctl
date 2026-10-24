{
  pkgs,
  njuctl,
  modules,
}:
let
  inherit (pkgs) lib;
  evaluated = lib.evalModules {
    modules = [ ./module.nix ] ++ modules;
    specialArgs = { inherit pkgs; };
  };
  cfg = evaluated.config;
  json = pkgs.writeText "njuctl-reservations.json" (
    builtins.toJSON {
      reservations = lib.mapAttrsToList (name: value: { inherit name; } // value) cfg.reservations;
    }
  );
  package = pkgs.writeShellApplication {
    name = "njuctl";
    text = ''
      ${lib.optionalString (cfg.passwordFile == null && cfg.passwordCommand != null) ''
        if [[ -z ''${NJU_PASSWORD:-} ]]; then
          NJU_PASSWORD="$(
            ${cfg.passwordCommand}
          )"
          export NJU_PASSWORD
        fi
      ''}
      exec ${njuctl}/bin/njuctl \
        --router ${lib.escapeShellArg cfg.router} \
        --username ${lib.escapeShellArg cfg.username} \
        --config ${json} \
        ${
          lib.optionalString (
            cfg.passwordFile != null
          ) "--password-file ${lib.escapeShellArg cfg.passwordFile}"
        } \
        "$@"
    '';
  };
in
assert lib.assertMsg (
  cfg.passwordFile == null || cfg.passwordCommand == null
) "njuctl: configure passwordFile or passwordCommand, not both";
{
  inherit json package;
  config = cfg;
  app = {
    type = "app";
    program = "${package}/bin/njuctl";
  };
}
