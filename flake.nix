# flake.nix
{
  description = "chronosweep: inbox sweep/audit/lint for Gmail";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages = {
          chronosweep-sweep = pkgs.buildGoModule {
            pname = "chronosweep-sweep";
            version = "0.1.0";
            src = ./.;
            subPackages = [ "cmd/chronosweep-sweep" ];
            vendorHash = null; # prefer to vendor and pin later
          };
          chronosweep-audit = pkgs.buildGoModule {
            pname = "chronosweep-audit";
            version = "0.1.0";
            src = ./.;
            subPackages = [ "cmd/chronosweep-audit" ];
            vendorHash = null;
          };
          chronosweep-lint = pkgs.buildGoModule {
            pname = "chronosweep-lint";
            version = "0.1.0";
            src = ./.;
            subPackages = [ "cmd/chronosweep-lint" ];
            vendorHash = null;
          };
          default = pkgs.symlinkJoin {
            name = "chronosweep";
            paths = [
              self.packages.${system}.chronosweep-sweep
              self.packages.${system}.chronosweep-audit
              self.packages.${system}.chronosweep-lint
            ];
          };
        };

        apps = {
          sweep = flake-utils.lib.mkApp { drv = self.packages.${system}.chronosweep-sweep; };
          audit = flake-utils.lib.mkApp { drv = self.packages.${system}.chronosweep-audit; };
          lint = flake-utils.lib.mkApp { drv = self.packages.${system}.chronosweep-lint; };
          default = self.apps.${system}.sweep;
        };

        devShells.default = pkgs.mkShell {
          buildInputs = [
            pkgs.go
            pkgs.golangci-lint
            pkgs.go-tools
          ];
        };
      }
    );
}
