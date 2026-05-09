{
  description = "Read-only bash classifier and runner";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [
        "aarch64-darwin"
        "x86_64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in
    {
      packages = forAllSystems (system:
        let
          pkgs = import nixpkgs { inherit system; };
        in
        {
          default = pkgs.callPackage ./package.nix { };
          readonly-bash = self.packages.${system}.default;
        });

      checks = forAllSystems (system: {
        default = self.packages.${system}.default;
      });

      lib.mkPackage = { pkgs, defaultConfigPath ? "" }:
        pkgs.callPackage ./package.nix { inherit defaultConfigPath; };
    };
}
