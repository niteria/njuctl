{
  description = "Sagemcom Funbox DHCP reservation utility";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
  outputs =
    { self, nixpkgs, ... }:
    let
      forAllSystems = nixpkgs.lib.genAttrs [
        "x86_64-linux"
        "aarch64-linux"
      ];
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          package = pkgs.buildGoModule {
            pname = "njuctl";
            version = "0.1.0";
            src = pkgs.lib.cleanSourceWith {
              src = ./.;
              filter =
                path: type: type == "directory" || pkgs.lib.hasSuffix ".go" path || baseNameOf path == "go.mod";
            };
            vendorHash = null;
            meta = {
              description = "Sagemcom Funbox DHCP reservation utility";
              homepage = "https://github.com/niteria/njuctl";
              license = pkgs.lib.licenses.mit;
              mainProgram = "njuctl";
            };
          };
        in
        {
          default = package;
          njuctl = package;
        }
      );
      apps = forAllSystems (
        system:
        let
          app = {
            type = "app";
            program = "${self.packages.${system}.njuctl}/bin/njuctl";
          };
        in
        {
          default = app;
          njuctl = app;
        }
      );
      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell { packages = [ pkgs.go ]; };
        }
      );
    };
}
