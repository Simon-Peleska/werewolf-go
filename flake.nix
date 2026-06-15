{
  description = "Werewolf";

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

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      go-test-tui,
      mcp-dap-server-src,
    }:
    # nixosModules is system-independent — lives outside eachDefaultSystem.
    {
      nixosModules.werewolf = import ./nixos-module.nix;
    }
    // flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # mcp-dap-server: official Delve MCP server (go-delve/mcp-dap-server)
        # After `nix flake update`, run `nix develop` — it will fail with the correct
        # vendorHash; replace the placeholder below with the "got:" hash from the error.
        mcp-dap-server = pkgs.buildGoModule {
          pname = "mcp-dap-server";
          version = "unstable";
          src = mcp-dap-server-src;
          vendorHash = "sha256-RpofdCGXwakl+ouhPEjrPjB+4uLhNrPNFpztEOxaJf0=";
          # Tests require dlv in PATH which isn't available in the Nix sandbox
          doCheck = false;
        };

        # Installs bash completions for the tools/*.sh scripts via installShellFiles
        # so bash-completion can lazy-load them through XDG_DATA_DIRS.
        tools-completions =
          pkgs.runCommand "werewolf-tools-completions"
            {
              nativeBuildInputs = [ pkgs.installShellFiles ];
            }
            ''
              installShellCompletion --cmd run_server.sh    --bash ${./tools/completions.bash}
              installShellCompletion --cmd run_tests.sh     --bash ${./tools/completions.bash}
              installShellCompletion --cmd start_chromium.sh --bash ${./tools/completions.bash}
            '';

      in
      {
        # `nix build` / `nix run`
        packages.default = pkgs.buildGoModule {
          pname = "werewolf";
          version = "0.0.1";
          src = ./.;

          # Pure-Go sqlite driver (modernc.org/sqlite) — no CGO, static binary.
          env.CGO_ENABLED = "0";

          # vendor/ directory is committed — set null to use it directly.
          vendorHash = null;

          # Inject the git revision; self.shortRev is only set on a clean tree.
          ldflags = [ "-X main.buildVersion=${self.shortRev or "dirty"}" ];
        };

        # `nix build .#docker && docker load < result`
        packages.docker = pkgs.dockerTools.buildLayeredImage {
          name = "werewolf";
          tag = "latest";
          contents = with pkgs; [
            self.packages.${system}.default
            cacert # for outbound HTTPS (e.g. AI storyteller providers)
          ];
          config = {
            Cmd = [ "/bin/werewolf" ];
            ExposedPorts = {
              "8080/tcp" = { };
            };
          };
        };

        apps.default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/werewolf";
        };

        # `nix develop`
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            sqlite # CLI for inspecting test/dev *.db files

            # Tool script deps
            inotify-tools # run_server.sh --watch
            chromium # start_chromium.sh manual testing
            jq # run_tests.sh per-test log splitting

            # Go debugger
            delve

            # Delve MCP server (AI-assisted debugging)
            mcp-dap-server

            # Test runner TUI
            go-test-tui.packages.${system}.default

            # AST-based code search and rewrite
            ast-grep

            # Image encoding for gen_seals.sh (cwebp / dwebp)
            libwebp

            # Bash completions for tools/*.sh and go-test-tui
            tools-completions
          ];

          CGO_ENABLED = "0";

          shellHook = ''
            # Make tools/*.sh scripts callable without the path prefix
            export PATH="$PWD/tools:$PATH"

            # Register completion share dirs so bash-completion lazy-loads them.
            export XDG_DATA_DIRS="${tools-completions}/share:${
              go-test-tui.packages.${system}.default
            }/share:''${XDG_DATA_DIRS:-/usr/local/share:/usr/share}"

            echo "Werewolf dev shell"
            echo "  run_server.sh      - start dev server"
            echo "  run_tests.sh       - run tests"
            echo "  start_chromium.sh  - open browser windows for manual testing"
            echo "  go build ./...     - build"
            echo "  run_tests.sh run   - run tests (stream to terminal)"
            echo "  sg / ast-grep      - AST-based code search and rewrite"
          '';
        };
      }
    );
}
