{
  description = "E2B Development environment";

  # flake inputs
  inputs.flake-utils.url      = "github:numtide/flake-utils";
  inputs.nixpkgs.url          = "github:nixos/nixpkgs/nixos-25.05";
  inputs.nixpkgs-unstable.url = "github:nixos/nixpkgs/nixos-unstable";
  inputs.terraform.url        = "github:stackbuilders/nixpkgs-terraform";

  outputs = { self, flake-utils, nixpkgs, nixpkgs-unstable, terraform }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs         = import nixpkgs {
          inherit system;
          config.allowUnfree = true;
        };
        unstablePkgs = import nixpkgs-unstable {
          inherit system;
          config.allowUnfree = true;
        };

        # E2B CLI wrapper
        e2b-cli = pkgs.writeShellScriptBin "e2b" ''
          exec ${pkgs.nodejs}/bin/npx @e2b/cli@latest "$@"
        '';

        # assemble notifier bits
        fileNotifier = with pkgs; lib.optional stdenv.isLinux libnotify
           ++ lib.optional stdenv.isLinux inotify-tools
           ++ lib.optional stdenv.isDarwin terminal-notifier
           ++ lib.optionals stdenv.isDarwin (
                with darwin.apple_sdk.frameworks; [
                  CoreFoundation
                  CoreServices
                ]
              );
      in {
        devShell = pkgs.mkShell {
          buildInputs = with pkgs; [
            bashInteractive
            git
            packer
            terraform.packages.${system}."1.5"
            google-cloud-sdk
            go
            docker
            cloudflared
            postgresql
            nodejs
            nodePackages.npm
            e2b-cli
          ] ++ fileNotifier;
        };
      }
    );
}
