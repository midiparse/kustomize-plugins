{
  buildGoModule,
  fetchFromGitHub,
  stdenv,
  lib,
  vendorHash,
  version,
}:

buildGoModule rec {
  pname = "kustomize-plugins";
  inherit vendorHash version;

  src = ./.;

  allowGoReference = true;
  env.CGO_ENABLED = "0";

  ldflags = [
    "-s"
    "-w"
  ];

  subPackages = [
    "cmd/resourceinjector"
    "cmd/vendorkustomization"
    "cmd/yqtransform"
  ];

  checkPhase = ''
    make test
  '';

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
    license = lib.licenses.mit;
  };
}
