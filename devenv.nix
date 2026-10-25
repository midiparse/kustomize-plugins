{
  pkgs,
  inputs,
  lib,
  config,
  ...
}:
{
  imports = [
    inputs.bikeshed.devenvModules.recommended
    inputs.bikeshed.devenvModules.go
  ];

  options = {
    version = lib.mkOption {
      type = pkgs.lib.types.str;
      default = "develop";
      description = "Version number for the package.";
    };
  };

  config = {
    enterShell = ''
      export PATH="$DEVENV_ROOT/bin:$PATH"
      go version
    '';

    enterTest = ''
      make test
    '';

    git-hooks.hooks = {
      nixfmt.enable = true;

      markdownlint.enable = true;

      golangci-lint.enable = true;

      golangci-lint-fmt = {
        enable = true;
        name = "golangci-lint fmt";
        entry = "${pkgs.golangci-lint}/bin/golangci-lint fmt";
        files = "\\.go$";
        before = [ "golangci-lint" ];
      };

      prettier = {
        enable = true;
        types_or = [
          "json"
          "yaml"
        ];
        excludes = [ "^test/.*/out\\.yaml$" ];
      };
    };

    profiles.agents.module = {
      imports = [
        inputs.bikeshed.devenvModules.agents
      ];

      agents = {
        skills = {
          enable = true;
          entries = {
            cc-skills-golang = (
              inputs.bikeshed.packages.${pkgs.system}.agentskills.cc-skills-golang.override {
                include = [
                  "golang-cli"
                  "golang-code-style"
                  "golang-data-structures"
                  "golang-error-handling"
                  "golang-testing"
                  "golang-lint"
                ];
              }
            );
          };
        };
      }
      // (lib.genAttrs inputs.bikeshed.lib.agents.popularHarnesses (_: {
        enable = true;
        linkSkills = true;
      }));
    };

    outputs =
      let
        params = {
          vendorHash = "sha256-n+lKbSCktXNgqTLSG++AWdItai7jDFGSAJTQs5hC23w=";
          version = config.version;
        };
      in
      inputs.flake-utils.lib.eachDefaultSystem (
        system:
        let
          stdenv = (
            pkgs.stdenv
            // {
              hostPlatform = pkgs.lib.systems.elaborate system;
            }
          );
          buildGoModule = pkgs.buildGoModule.override { inherit stdenv; };
        in
        {
          default = (
            (pkgs.callPackage ./pkg.nix (params // { inherit buildGoModule; })).overrideAttrs (old: {
              env = stdenv.hostPlatform.go // {
                CGO_ENABLED = "0";
              };
              doCheck = false;
            })
          );
        }
      );
  };
}
