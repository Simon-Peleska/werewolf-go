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
    mcp-dap-server-src = {
      url = "github:go-delve/mcp-dap-server";
      flake = false;
    };
  };

  outputs = { self, nixpkgs, flake-utils, go-test-tui, mcp-dap-server-src }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # CGO is required for go-sqlite3
        cgoBuildInputs = with pkgs; [ sqlite ];
        cgoNativeBuildInputs = with pkgs; [ gcc pkg-config ];

        # mcp-dap-server: official Delve MCP server (go-delve/mcp-dap-server)
        # After `nix flake update`, run `nix develop` — it will fail with the correct
        # vendorHash; replace the placeholder below with the "got:" hash from the error.
        mcp-dap-server = pkgs.buildGoModule {
          pname = "mcp-dap-server";
          version = "unstable";
          src = mcp-dap-server-src;
          # Relax go.mod version constraint so nixpkgs Go 1.25 can build it
          postPatch = ''
            substituteInPlace go.mod --replace-fail 'go 1.26.1' 'go 1.25'
          '';
          vendorHash = "sha256-RpofdCGXwakl+ouhPEjrPjB+4uLhNrPNFpztEOxaJf0=";
          # Tests require dlv in PATH which isn't available in the Nix sandbox
          doCheck = false;
        };

        # Installs bash completions for the tools/*.sh scripts via installShellFiles
        # so bash-completion can lazy-load them through XDG_DATA_DIRS.
        tools-completions = pkgs.runCommand "werewolf-tools-completions" {
          nativeBuildInputs = [ pkgs.installShellFiles ];
        } ''
          installShellCompletion --cmd run_server.sh    --bash ${./tools/completions.bash}
          installShellCompletion --cmd run_tests.sh     --bash ${./tools/completions.bash}
          installShellCompletion --cmd start_chromium.sh --bash ${./tools/completions.bash}
        '';

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

            # Go debugger
            delve

            # Delve MCP server (AI-assisted debugging)
            mcp-dap-server

            # Test runner TUI
            go-test-tui.packages.${system}.default

            # Bash completions for tools/*.sh and go-test-tui
            tools-completions
          ];

          # CGO flags so `go build` / `go test` work inside the shell
          CGO_ENABLED = "1";

          shellHook = ''
            # Make tools/*.sh scripts callable without the path prefix
            export PATH="$PWD/tools:$PATH"

            # Register completion share dirs so bash-completion lazy-loads them.
            export XDG_DATA_DIRS="${tools-completions}/share:${go-test-tui.packages.${system}.default}/share:''${XDG_DATA_DIRS:-/usr/local/share:/usr/share}"

            echo "Werewolf dev shell"
            echo "  run_server.sh      - start dev server"
            echo "  run_tests.sh       - run tests"
            echo "  start_chromium.sh  - open browser windows for manual testing"
            echo "  go build ./...     - build"
            echo "  run_tests.sh run   - run tests (stream to terminal)"
          '';
        };
      });
}
