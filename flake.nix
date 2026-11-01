{
  description = "Kustomize plugins";

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
    let
      targetSystems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];
    in
    flake-utils.lib.eachSystem
      (builtins.filter (s: s != "x86_64-darwin") flake-utils.lib.defaultSystems)
      (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};

          buildForTarget =
            targetSystem:
            let
              targetPlatform = pkgs.lib.systems.elaborate targetSystem;
              targetGo = targetPlatform.go;
              isCross = targetSystem != system;
              go = pkgs.go // {
                inherit (targetGo) GOOS GOARCH;
                CGO_ENABLED = 0;
              };
              stdenv = pkgs.stdenv // {
                hostPlatform = targetPlatform;
              };
              buildGoModule = pkgs.buildGoModule.override { inherit go stdenv; };
            in
            buildGoModule rec {
              pname = "kustomize-plugins";
              version = "latest";
              src = pkgs.lib.cleanSource ./.;

              vendorHash = "sha256-n+lKbSCktXNgqTLSG++AWdItai7jDFGSAJTQs5hC23w=";

              allowGoReference = true;
              doCheck = !isCross;
              env.CGO_ENABLED = 0;

              ldflags = [
                "-s"
                "-w"
              ];

              subPackages = [
                "cmd/resourceinjector"
                "cmd/vendorkustomization"
                "cmd/yqtransform"
              ];

              postInstall = ''
                if [ -d "$out/bin" ]; then
                  for f in "$out/bin"/*; do
                    [ -e "$f" ] || continue
                    base="$(basename "$f")"
                    mv "$f" "$out/bin/kustomize-plugin-$base"
                  done
                fi

                install -d "$out/share/kustomize-plugins/schema"
                install -m644 schema/*.json "$out/share/kustomize-plugins/schema/"
              '';

              meta = {
                license = pkgs.lib.licenses.mit;
              };
            };

          kustomize-plugins = buildForTarget system;
        in
        {
          packages = {
            default = kustomize-plugins;
            inherit kustomize-plugins;
          }
          // pkgs.lib.genAttrs targetSystems (target: buildForTarget target);
        }
      );
}
