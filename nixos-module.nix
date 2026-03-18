# NixOS module for the werewolf game server.
# Exposed as werewolf-go.nixosModules.werewolf in flake.nix.
# The server flake imports this and sets services.werewolf.* options.
#
# Secrets (API keys etc.) go in /etc/werewolf/config.json on the server.
# Create it manually — it is never part of the Nix store.
# Example /etc/werewolf/config.json:
#   {
#     "storyteller_provider": "openai",
#     "storyteller_api_key": "sk-...",
#     "narrator_api_key": "sk-..."
#   }
{ config, lib, pkgs, ... }:

let
  cfg = config.services.werewolf;
in {
  options.services.werewolf = {
    enable = lib.mkEnableOption "Werewolf game server";

    package = lib.mkOption {
      type = lib.types.package;
      description = "The werewolf binary package.";
    };

    listenAddr = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1:8080";
      description = "Internal address the game server binds to (nginx proxies to this).";
    };

    configFile = lib.mkOption {
      type = lib.types.str;
      default = "/etc/werewolf/config.json";
      description = ''
        Path to the JSON config file containing settings and secrets.
        Create this file manually on the server; it is never part of the Nix store.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    # Dedicated unprivileged user.
    users.users.werewolf = {
      isSystemUser = true;
      group        = "werewolf";
      description  = "Werewolf game server";
    };
    users.groups.werewolf = {};

    systemd.services.werewolf = {
      description = "Werewolf game server";
      wantedBy    = [ "multi-user.target" ];
      after       = [ "network.target" ];

      environment = {
        ADDR = cfg.listenAddr;
        # WAL mode is important for SQLite under concurrent WebSocket load.
        DB = "file:/var/lib/werewolf/werewolf.db?cache=shared&_journal_mode=WAL";
      };

      serviceConfig = {
        User  = "werewolf";
        Group = "werewolf";

        ExecStart  = "${cfg.package}/bin/werewolf -config ${cfg.configFile}";
        Restart    = "on-failure";
        RestartSec = "5s";

        # systemd creates and owns /var/lib/werewolf, no manual mkdir needed.
        StateDirectory     = "werewolf";
        StateDirectoryMode = "0750";
        WorkingDirectory   = "/var/lib/werewolf";

        # Basic hardening.
        NoNewPrivileges = true;
        PrivateTmp      = true;
        ProtectSystem   = "strict";
        ProtectHome     = true;
        ReadWritePaths  = [ "/var/lib/werewolf" ];
        # Allow reading the config file from /etc/werewolf.
        ReadOnlyPaths  = [ "/etc/werewolf" ];
      };
    };

  };
}
