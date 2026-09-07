{
  description = "Kubernetes controller development environment using Go and Kubebuilder";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-parts = {
      url = "github:hercules-ci/flake-parts";
      inputs.nixpkgs-lib.follows = "nixpkgs";
    };
    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = inputs@{ flake-parts, treefmt-nix, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      imports = [
        treefmt-nix.flakeModule
      ];

      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      perSystem = { pkgs, ... }:
        let
          # Symlink etcd and kube-apiserver into a unified path for envtest
          envtestAssets = pkgs.symlinkJoin {
            name = "envtest-assets";
            paths = [
              pkgs.etcd
              pkgs.kubernetes
            ];
          };
        in
        {

          treefmt = {
            projectRootFile = "flake.nix";
            programs.gofmt.enable = true;
            programs.yamlfmt.enable = true;
            programs.nixpkgs-fmt.enable = true;

            settings.global.excludes = [
              "vendor/*"
              "bin/*"
              "config/*"
              "config/**"
            ];
          };

          devShells.default = pkgs.mkShell {
            packages = with pkgs; [
              go
              gopls
              golangci-lint
              gotools # goimports, stringer, etc.

              kubebuilder
              kustomize
              kubectl
              kind
              # controller-tools # Provides `controller-gen`

              etcd
              kubernetes
            ];

            # Configures envtest to use hermetic Nix-provided etcd and apiserver
            # binaries instead of attempting to download unpatched glibc tarballs at runtime.
            shellHook = ''
              export KUBEBUILDER_ASSETS="${envtestAssets}/bin"
              echo "🚀 Kubebuilder environment ready."
              echo "KUBEBUILDER_ASSETS -> $KUBEBUILDER_ASSETS"
            '';
          };
        };
    };
}
