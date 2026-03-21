# NixOS module for the werewolf game server.
# Exposed as werewolf-go.nixosModules.werewolf in flake.nix.
#
# Non-secret settings are declared as module options and passed as environment
# variables. Secrets (API keys) go in an environmentFile — a plain text file
# with KEY=value lines, never part of the Nix store.
#
# Minimal /etc/werewolf/secrets on the server:
#   OPENAI_API_KEY=sk-...
#   NARRATOR_API_KEY=sk-...
{ config, lib, pkgs, ... }:

let
  cfg = config.services.werewolf;

  # Build the environment attrset from non-null options.
  optionalEnv = name: val:
    lib.optionalAttrs (val != null) { ${name} = val; };
in {
  options.services.werewolf = {
    enable = lib.mkEnableOption "Werewolf game server";

    package = lib.mkOption {
      type = lib.types.package;
      description = "The werewolf binary package.";
    };

    listenAddr = lib.mkOption {
      type    = lib.types.str;
      default = "127.0.0.1:8080";
      description = "Internal address the game server binds to (nginx proxies to this).";
    };

    environmentFile = lib.mkOption {
      type    = lib.types.nullOr lib.types.path;
      default = null;
      description = ''
        Path to a file containing secret environment variables (KEY=value lines).
        Create this file manually on the server — it is never part of the Nix store.
        Typically contains OPENAI_API_KEY and NARRATOR_API_KEY.
      '';
    };

    # ── Storyteller ───────────────────────────────────────────────────────────
    storyteller = lib.mkOption {
      type    = lib.types.bool;
      default = false;
      description = "Enable AI storyteller.";
    };
    openaiModel = lib.mkOption {
      type    = lib.types.nullOr lib.types.str;
      default = null;
      description = "OpenAI model name.";
    };
    openaiApiBase = lib.mkOption {
      type    = lib.types.nullOr lib.types.str;
      default = null;
      description = "OpenAI API base URL (default: https://api.openai.com/v1).";
    };
    storytellerTemperature = lib.mkOption {
      type    = lib.types.nullOr lib.types.str;
      default = null;
      description = "Sampling temperature (0-2, default 1.2).";
    };

    # ── Narrator (TTS) ────────────────────────────────────────────────────────
    narratorProvider = lib.mkOption {
      type    = lib.types.nullOr lib.types.str;
      default = null;
      description = "Narrator provider: openai|openai-compatible|elevenlabs.";
    };
    narratorModel = lib.mkOption {
      type    = lib.types.nullOr lib.types.str;
      default = null;
      description = "TTS model name.";
    };
    narratorVoice = lib.mkOption {
      type    = lib.types.nullOr lib.types.str;
      default = null;
      description = "Voice name or ElevenLabs voice ID.";
    };
    narratorUrl = lib.mkOption {
      type    = lib.types.nullOr lib.types.str;
      default = null;
      description = "Base URL for openai-compatible TTS.";
    };
  };

  config = lib.mkIf cfg.enable {
    # Create /etc/werewolf/ and an empty secrets file if they don't exist.
    # `f` = create file only if absent (never overwrites).
    systemd.tmpfiles.rules = [
      "d /etc/werewolf 0750 root werewolf - -"
      "f /etc/werewolf/secrets 0640 root werewolf - -"
    ];

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
      }
      // (if cfg.storyteller then { STORYTELLER = "true"; } else {})
      // optionalEnv "OPENAI_MODEL"            cfg.openaiModel
      // optionalEnv "OPENAI_API_BASE"         cfg.openaiApiBase
      // optionalEnv "STORYTELLER_TEMPERATURE" cfg.storytellerTemperature
      // optionalEnv "NARRATOR_PROVIDER"       cfg.narratorProvider
      // optionalEnv "NARRATOR_MODEL"          cfg.narratorModel
      // optionalEnv "NARRATOR_VOICE"          cfg.narratorVoice
      // optionalEnv "NARRATOR_URL"            cfg.narratorUrl;

      restartIfChanged = true;

      serviceConfig = {
        User  = "werewolf";
        Group = "werewolf";

        ExecStart  = "${cfg.package}/bin/werewolf";
        Restart    = "on-failure";
        RestartSec = "5s";

        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) "-${cfg.environmentFile}";

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
      };
    };
  };
}
