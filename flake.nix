{
  description = "Werewolf - social deduction game in Go";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    go-test-tui = {
      url = "github:Simon-Peleska/go-test-tui";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.follows = "flake-utils";
    };
  };

  outputs = { self, nixpkgs, flake-utils, go-test-tui }:
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

          env.CGO_ENABLED = "1";
          nativeBuildInputs = cgoNativeBuildInputs;
          buildInputs = cgoBuildInputs;

          # Run `nix build` once — it will print the correct hash.
          # Replace the placeholder below with the "got:" hash from the error.
          vendorHash = "sha256-7L92A2x0TNbnPFgPQzIGZApHzUe6nYOIi3HqxxxtLBs=";
        };

        # `nix build .#docker && docker load < result`
        packages.docker = pkgs.dockerTools.buildLayeredImage {
          name = "werewolf";
          tag = "latest";
          contents = with pkgs; [
            self.packages.${system}.default
            sqlite
            glibc
            cacert  # for outbound HTTPS (e.g. AI storyteller providers)
          ];
          config = {
            Cmd = [ "/bin/werewolf" ];
            ExposedPorts = { "8080/tcp" = {}; };
          };
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
            jq             # run_tests.sh per-test log splitting

            # Test runner TUI
            go-test-tui.packages.${system}.default
          ];

          # CGO flags so `go build` / `go test` work inside the shell
          CGO_ENABLED = "1";

          shellHook = ''
            # Load tab completions for tools/*.sh scripts
            if [ -f "$PWD/tools/completions.bash" ]; then
              source "$PWD/tools/completions.bash"
            fi

            # go-test-tui completions (sourced from flake input source tree)
            if [ -f "${go-test-tui}/completions.bash" ]; then
              source "${go-test-tui}/completions.bash"
            fi

            # Make tools/*.sh scripts callable without the path prefix
            export PATH="$PWD/tools:$PATH"

            # Register completion files via XDG so bash-completion lazy-loads
            # them per-command (works in nix develop and nix-direnv)
            export XDG_DATA_DIRS="$PWD/tools/share:''${XDG_DATA_DIRS:-/usr/local/share:/usr/share}"

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
