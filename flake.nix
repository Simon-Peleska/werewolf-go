{
  description = "Werewolf - social deduction game in Go";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # CGO is required for go-sqlite3
        cgoBuildInputs = with pkgs; [ sqlite ];
        cgoNativeBuildInputs = with pkgs; [ gcc pkg-config ];

      in {
        # `nix build` / `nix run`
        packages.default = pkgs.buildGoModule {
          pname = "werewolf";
          version = "0.0.1";
          src = ./.;

          CGO_ENABLED = "1";
          nativeBuildInputs = cgoNativeBuildInputs;
          buildInputs = cgoBuildInputs;

          # Run `nix build` once — it will print the correct hash.
          # Replace the placeholder below with the "got:" hash from the error.
          vendorHash = pkgs.lib.fakeHash;
        };

        apps.default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/werewolf";
        };

        # `nix develop`
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            # Go toolchain + CGO deps
            go
            gcc
            pkg-config
            sqlite

            # Tool script deps
            inotify-tools  # run_server.sh --watch
            chromium       # start_chromium.sh manual testing
          ];

          # CGO flags so `go build` / `go test` work inside the shell
          CGO_ENABLED = "1";

          shellHook = ''
            # Load tab completions for tools/*.sh scripts
            if [ -f "$PWD/tools/completions.bash" ]; then
              source "$PWD/tools/completions.bash"
            fi

            # Make tools/*.sh scripts callable without the path prefix
            export PATH="$PWD/tools:$PATH"

            echo "Werewolf dev shell"
            echo "  run_server.sh      - start dev server"
            echo "  run_tests.sh       - run tests"
            echo "  start_chromium.sh  - open browser windows for manual testing"
            echo "  go build ./...     - build"
            echo "  go test ./...      - test"
          '';
        };
      });
}
