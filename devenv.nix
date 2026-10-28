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
    packages = [
      pkgs.nix-update
    ];

    scripts.update-vendor-hash.exec = ''
      exec nix-update --flake --version=skip kustomize-plugins "$@"
    '';

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

      update-vendor-hash = {
        enable = true;
        name = "update-vendor-hash";
        entry = "${pkgs.nix-update}/bin/nix-update --flake --version=skip kustomize-plugins";
        files = "go\\.(mod|sum)$";
        pass_filenames = false;
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
        flake = (import ./flake.nix).outputs {
          self = { };
          inherit (inputs) nixpkgs flake-utils;
        };
      in
      inputs.flake-utils.lib.eachSystem (builtins.attrNames flake.packages) (system: {
        default = flake.packages.${system}.default;
      });
  };
}
